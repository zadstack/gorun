package kernel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

const (
	settingsModule = "settings"
	jobWorkers     = 2
)

// reservedNames are kernel-owned migration namespaces no module may use.
var reservedNames = []string{"db", "i18n", "audit", "jobs", "diagnostics"}

// MigrateAll validates the module list like Boot, then applies kernel
// infrastructure migrations (audit, jobs, diagnostics) and every module's migrations, in
// order. Used by `gorun migrate`.
func MigrateAll(ctx context.Context, d *sql.DB, mods []Module) error {
	if err := validateModules(mods); err != nil {
		return err
	}
	all := mods
	var err error
	mods, err = enabledModules(ctx, d, mods)
	if err != nil {
		return err
	}
	reports, err := migrateInfra(ctx, d)
	if err != nil {
		return err
	}
	for _, m := range mods {
		rep, err := db.MigrateWithReport(ctx, d, m.Name(), m.Migrations())
		if err != nil {
			return err
		}
		reports = append(reports, rep)
	}
	warnChecksumMismatches(slog.Default(), reports)
	warnSchemaAhead(ctx, slog.Default(), d, all)
	return nil
}

// infraMigrations are the kernel-owned migration sets, in apply order.
func infraMigrations() []struct {
	name  string
	files fs.FS
} {
	return []struct {
		name  string
		files fs.FS
	}{{"audit", audit.Migrations()}, {"jobs", jobs.Migrations()}, {"diagnostics", diagnostics.Migrations()}}
}

func migrateInfra(ctx context.Context, d *sql.DB) ([]db.MigrateReport, error) {
	var reports []db.MigrateReport
	for _, m := range infraMigrations() {
		rep, err := db.MigrateWithReport(ctx, d, m.name, m.files)
		if err != nil {
			return nil, err
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

// BootOptions tunes BootWithOptions.
type BootOptions struct {
	// StartJobs starts the job workers and cron scheduler (Boot does). CLI
	// commands that may run beside a live server (reindex --no-jobs) leave
	// it false: workers would requeue the server's running jobs as stale
	// and duplicate its cron ticks. Jobs enqueued meanwhile wait for the
	// server's workers.
	//
	// A process that starts job workers first takes the exclusive
	// data-directory lock (<data>/gorun.lock, see LockDataDir) and refuses
	// to boot when another process holds it; App.Close releases it.
	StartJobs bool
}

// Boot opens the database and wires GoRun in this order: migrate infrastructure
// → settings module migrate+register → load i18n (core catalog plus
// every CatalogProvider module) → remaining modules migrate+register in the
// given order → mount routers → start job workers. mods must contain the
// settings module and follow BootOrder.
func Boot(ctx context.Context, cfg config.Config, mods []Module) (*App, error) {
	return BootWithOptions(ctx, cfg, mods, BootOptions{StartJobs: true})
}

// BootWithOptions is Boot with options (see BootOptions).
func BootWithOptions(ctx context.Context, cfg config.Config, mods []Module, opts BootOptions) (*App, error) {
	if _, err := cfg.TrustedProxyNetworks(); err != nil {
		return nil, err
	}
	if _, err := cfg.PublicScriptSources(); err != nil {
		return nil, err
	}
	if err := validateModules(mods); err != nil {
		return nil, err
	}
	var lock *DataLock
	if opts.StartJobs {
		var err error
		if lock, err = LockDataDir(cfg.DataDir); err != nil {
			return nil, err
		}
	}
	fail := func(d *sql.DB, err error) (*App, error) {
		if d != nil {
			d.Close()
		}
		_ = lock.Release()
		return nil, err
	}
	d, err := db.Open(cfg.DBPath())
	if err != nil {
		return fail(nil, err)
	}
	all := mods // disabled add-ons' applied migrations are still known
	mods, err = enabledModules(ctx, d, mods)
	if err != nil {
		return fail(d, err)
	}
	deps, err := wire(ctx, d, cfg, mods, all)
	if err != nil {
		return fail(d, err)
	}
	collector, _ := Get[*diagnostics.Collector](deps, diagnostics.ServiceName)
	collector.Start()
	handler := assemble(deps)
	jobsCtx, cancel := context.WithCancel(ctx)
	if opts.StartJobs {
		deps.Jobs.Start(jobsCtx, jobWorkers)
		deps.Log.Info("jobs: workers started", "workers", jobWorkers)
	} else {
		deps.Log.Info("jobs: workers not started (this process only enqueues; a running server executes the jobs)")
	}
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			cancel()
			deps.Jobs.Wait()
			collector.Close()
			if err := d.Close(); err != nil {
				deps.Log.Error("close db", "err", err)
			}
			if err := lock.Release(); err != nil {
				deps.Log.Error("release data directory lock", "err", err)
			}
		})
	}
	return &App{Deps: deps, Handler: handler, Close: closeFn}, nil
}

// wire migrates and registers mods (the enabled modules); all is the full
// module list, used to tell which applied migrations this binary knows.
func wire(ctx context.Context, d *sql.DB, cfg config.Config, mods, all []Module) (*Deps, error) {
	reports, err := migrateInfra(ctx, d)
	if err != nil {
		return nil, err
	}
	collector := diagnostics.New(d)
	log := slog.New(collector.Handler(newLogger(cfg).Handler()))
	deps := &Deps{
		DB:       d,
		Hooks:    hooks.New(),
		Jobs:     jobs.New(d, log),
		Settings: settings.New(d).WithLogger(log),
		Log:      log,
		Config:   cfg,
		Router:   &Router{Public: newLateRouter(), Admin: newLateRouter(), API: newLateRouter(), Webhooks: newLateRouter()},
		Admin:    NewAdminRegistry(),
		Theme:    NewThemeRegistry(),
		API:      NewAPIRegistry(),
		MCP:      NewMCPRegistry(),
		Perms:    NewPermissionRegistry(),
		Audit:    audit.New(d, log),
		Services: NewServiceRegistry(),
	}
	deps.Services.Set(diagnostics.ServiceName, collector)
	deps.Hooks.AttachQueue(deps.Jobs)
	guard := requireAuthenticator(deps.Router)
	deps.Router.Admin.Use(guard)
	deps.Router.API.Use(guard)

	idx := slices.IndexFunc(mods, func(m Module) bool { return m.Name() == settingsModule })
	rep, err := setup(ctx, deps, mods[idx])
	if err != nil {
		return nil, err
	}
	reports = append(reports, rep)
	bundle, err := i18n.Load(catalogs(mods)...)
	if err != nil {
		return nil, err
	}
	deps.I18n = bundle
	for i, m := range mods {
		if i == idx {
			continue
		}
		rep, err := setup(ctx, deps, m)
		if err != nil {
			return nil, err
		}
		reports = append(reports, rep)
	}
	warnChecksumMismatches(deps.Log, reports)
	warnSchemaAhead(ctx, deps.Log, d, all)
	deps.Services.Set(ServiceSchema, SchemaInspector(func(ctx context.Context) (db.SchemaInspection, error) {
		return InspectSchema(ctx, d, all)
	}))
	return deps, nil
}

// setup migrates then registers one module, converting a Register panic
// (e.g. a duplicate registration) into an error.
func setup(ctx context.Context, deps *Deps, m Module) (rep db.MigrateReport, err error) {
	rep, err = db.MigrateWithReport(ctx, deps.DB, m.Name(), m.Migrations())
	if err != nil {
		return rep, err
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("register %s: panic: %v", m.Name(), p)
		}
	}()
	if err := m.Register(deps); err != nil {
		return rep, fmt.Errorf("register %s: %w", m.Name(), err)
	}
	return rep, nil
}

func catalogs(mods []Module) []fs.FS {
	var out []fs.FS
	for _, m := range mods {
		if cp, ok := m.(CatalogProvider); ok {
			if c := cp.Catalogs(); c != nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// validateModules requires unique names, the settings module, no reserved
// names, and relative BootOrder for listed modules (unlisted names, e.g.
// future extensions, may appear anywhere).
func validateModules(mods []Module) error {
	seen := map[string]bool{}
	last, lastName := -1, ""
	for _, m := range mods {
		name := m.Name()
		switch {
		case name == "":
			return errors.New("kernel: module with empty name")
		case seen[name]:
			return fmt.Errorf("kernel: duplicate module %q", name)
		case slices.Contains(reservedNames, name):
			return fmt.Errorf("kernel: module name %q is reserved for the kernel", name)
		}
		seen[name] = true
		if pos := slices.Index(BootOrder, name); pos >= 0 {
			if pos < last {
				return fmt.Errorf("kernel: module %q must come before %q (BootOrder)", name, lastName)
			}
			last, lastName = pos, name
		}
	}
	if !seen[settingsModule] {
		return errors.New("kernel: the settings module is required")
	}
	return nil
}

// requireAuthenticator denies every request until a module has called
// Router.SetAuthenticator, so Admin and API are never served unauthenticated.
func requireAuthenticator(r *Router) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if r.Authenticator() == "" {
				http.Error(w, "authentication module not installed", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

func plain(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

// assemble mounts the sub-routers on the root mux behind the shared httpx
// middleware (request id, client IP, logging, panic recovery, security
// headers). It must run after every module has registered (see Router).
func assemble(deps *Deps) http.Handler {
	r, d := deps.Router, deps.DB
	root := chi.NewRouter()
	proxies, _ := deps.Config.TrustedProxyNetworks()  // validated before boot
	scriptSrc, _ := deps.Config.PublicScriptSources() // validated before boot
	root.Use(httpx.RequestID, httpx.RealIPWithTrustedProxies(proxies), httpx.WarnUntrustedForwarding(deps.Log), httpx.Logger(deps.Log), httpx.Recover(deps.Log),
		httpx.SecurityHeadersWith(httpx.SecurityOptions{PublicScripts: scriptSrc,
			HSTSIncludeSubdomains: deps.Config.HSTSIncludeSubdomains, HSTSPreload: deps.Config.HSTSPreload}), httpx.Compress,
		// HEAD is answered by the GET route (no body) wherever no HEAD route exists.
		middleware.GetHead)
	root.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { plain(w, http.StatusOK, "ok") })
	root.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := d.PingContext(req.Context()); err != nil {
			plain(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		plain(w, http.StatusOK, "ok")
	})
	root.Mount("/admin", r.Admin)
	root.Mount("/api", r.API)
	root.Mount("/webhooks", r.Webhooks)
	root.Mount("/", r.Public)
	return root
}

// LogOutput is where Boot's logger writes (tests set io.Discard).
var LogOutput io.Writer = os.Stderr

func newLogger(cfg config.Config) *slog.Logger {
	if cfg.Env == "prod" {
		return slog.New(slog.NewJSONHandler(LogOutput, nil))
	}
	return slog.New(slog.NewTextHandler(LogOutput, nil))
}
