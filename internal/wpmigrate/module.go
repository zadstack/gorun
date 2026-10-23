// Package wpmigrate is the WordPress Migrator (spec §4.7): an honest
// Discovery Report, a 7-step wizard, a job-based import pipeline with
// resume/retry/dry-run, Gutenberg conversion, media download and URL
// rewrite, Yoast mapping, a WooCommerce catalog adapter and a WXR fallback.
package wpmigrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the wpmigrate kernel.Module.
type Module struct{}

// New returns the wpmigrate module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "wpmigrate" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

func (*Module) Enabled(ctx context.Context, database *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, database, "wpmigrate")
}

// Register publishes the Service, binds the pipeline jobs, the wizard
// (admin → System → Migrations), the /v1/migrations API and the health
// check.
func (*Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(Permission, map[string]string{"en": "Run migrations", "ar": "تشغيل عمليات الترحيل"})
	svc, err := newService(d)
	if err != nil {
		return err
	}
	svc.registerJobs(d.Jobs)
	d.Jobs.Cron("wpmigrate.watchdog", watchdogEvery, svc.watchdog)
	d.Services.Set(ServiceName, Service(svc))
	registerCommerceImporter(d)
	d.Hooks.Filter(admin.HealthCheckFilter, 60, func(ctx context.Context, v any) (any, error) {
		if list, ok := v.(*[]admin.HealthCheck); ok {
			*list = append(*list, svc.healthCheck(ctx))
		}
		return v, nil
	})
	if err := registerAdmin(d, svc); err != nil {
		return err
	}
	registerAPI(d, svc)
	return nil
}

// healthCheck reports the last migration's status.
func (s *service) healthCheck(ctx context.Context) admin.HealthCheck {
	tr := func(key string, args ...any) string {
		if s.d.I18n == nil {
			return key
		}
		return s.d.I18n.T(i18n.Locale(ctx), key, args...)
	}
	hc := admin.HealthCheck{Name: "wpmigrate", Status: admin.CheckOK, Detail: tr("wpmigrate.health.none")}
	var id, status string
	err := s.db.QueryRowContext(ctx, `SELECT id, status FROM migrations ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id, &status)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			hc.Status, hc.Detail = admin.CheckFail, tr("wpmigrate.health.unavailable")
			s.log.Error("wpmigrate: health query", "err", err)
		}
		return hc
	}
	hc.Detail = tr("wpmigrate.health.last", tr("wpmigrate.status."+status))
	var failed int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'failed'`, id).Scan(&failed); err != nil {
		hc.Status, hc.Detail = admin.CheckFail, tr("wpmigrate.health.unavailable")
		s.log.Error("wpmigrate: health item query", "err", err)
		return hc
	}
	switch {
	case status == StatusFailed:
		hc.Status = admin.CheckFail
	case failed > 0 || status == StatusPaused:
		hc.Status = admin.CheckWarn
		if failed > 0 {
			hc.Detail += " · " + tr("wpmigrate.health.failed_items", failed)
		}
	}
	return hc
}

// ImportWXRFile imports a WXR export synchronously (the `gorun
// import-wxr` command): it creates a WXR source and a migration, scans,
// runs the whole pipeline and validates. deps must come from a booted
// kernel with the content, taxonomy, media, menus and redirects modules.
func ImportWXRFile(ctx context.Context, deps *kernel.Deps, path string, opts Options) (*Result, error) {
	return ImportWXR(ctx, deps, WXRFile{Path: path}, opts)
}

// WXRFile describes a WXR export imported outside the wizard (the CLI).
type WXRFile struct {
	Path       string
	SourceName string // "" → "WXR <file name>"
	// ResetOptions replaces a reused migration's stored options with the
	// ones passed in; otherwise a re-run keeps them (OptionsKept reports
	// when the passed options were ignored).
	ResetOptions bool
	// Render, when set, answers the validate step's render checks
	// in-process (the CLI passes the booted app's handler, so no server has
	// to run); otherwise they go over HTTP to the configured base URL.
	Render http.Handler
}

// ImportWXR imports f synchronously: it creates a WXR source and a
// migration, scans, runs the whole pipeline and validates.
func ImportWXR(ctx context.Context, deps *kernel.Deps, f WXRFile, opts Options) (*Result, error) {
	run, q, m, src, kept, err := prepareWXR(ctx, deps, f, opts)
	if err != nil {
		return nil, err
	}
	if err := run.Start(ctx, m.ID); err != nil {
		return nil, err
	}
	if _, err := q.drain(ctx, 0); err != nil {
		return nil, err
	}
	st, err := run.Status(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	res := &Result{MigrationID: m.ID, SourceID: src.ID, Status: st.Status, Counts: st.Counts, OptionsKept: kept}
	if rep, err := run.report(ctx, m.ID); err == nil {
		res.Report = rep
	}
	if st.Status != StatusValidated && st.Status != StatusCompleted {
		return res, fmt.Errorf("wpmigrate: import ended with status %s", st.Status)
	}
	return res, nil
}

// PreviewWXR creates a WXR source and migration for f, scans it and runs
// the dry-run preview (no content is written). The source and migration
// stay listed in the wizard, where the import can be started later.
func PreviewWXR(ctx context.Context, deps *kernel.Deps, f WXRFile, opts Options) (*PreviewResult, error) {
	run, _, m, _, kept, err := prepareWXR(ctx, deps, f, opts)
	if err != nil {
		return nil, err
	}
	p, err := run.Preview(ctx, m.ID)
	if p != nil {
		p.OptionsKept = kept
	}
	return p, err
}

// prepareWXR creates the source and migration for a WXR file and scans it,
// on a service whose jobs run inline on the returned queue.
func prepareWXR(ctx context.Context, deps *kernel.Deps, f WXRFile, opts Options) (run *service, q *inlineQueue, m *MigrationView, src *SourceView, optionsKept bool, err error) {
	svc, ok := registered(deps)
	if !ok {
		enabled := deps.Config.Addons["wpmigrate"] != kernel.AddonModeDisabled
		if enabled {
			var gateErr error
			if enabled, gateErr = kernel.AddonEnabled(ctx, deps.DB, "wpmigrate"); gateErr != nil {
				return nil, nil, nil, nil, false, gateErr
			}
		}
		if !enabled {
			return nil, nil, nil, nil, false, errors.New("wpmigrate: addon disabled; set addons.wpmigrate.enabled=true and remove wpmigrate=disabled from GORUN_ADDONS or [addons], then run the import or preview command again")
		}
		var err error
		if svc, err = newService(deps); err != nil {
			return nil, nil, nil, nil, false, err
		}
	}
	name := f.SourceName
	if name == "" {
		name = "WXR " + baseName(f.Path)
	}
	q = newInlineQueue()
	run = q.bind(svc)
	if f.Render != nil {
		run.check = handlerCheck(f.Render)
	}
	// The same export (same file, explicit source name, or same site) reuses
	// its source and latest migration: a re-run updates, never duplicates.
	src, reused, err := run.wxrSourceFor(ctx, name, f.Path, f.SourceName != "")
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	if mid, ok := run.latestMigration(ctx, src.ID); reused && ok {
		if m, err = run.Migration(ctx, mid); err != nil {
			return nil, nil, nil, nil, false, err
		}
		switch {
		case f.ResetOptions:
			if err := run.setOptions(ctx, m.ID, opts); err != nil {
				return nil, nil, nil, nil, false, err
			}
			m.Options = opts.normalized()
		case opts != (Options{}) && opts.normalized() != m.Options.normalized():
			optionsKept = true
			run.logEntry(ctx, m.ID, "warn", "", "options_kept", map[string]any{"stored": m.Options.normalized(), "ignored": opts.normalized()})
		}
	} else if m, err = run.CreateMigration(ctx, src.ID, opts, ""); err != nil {
		return nil, nil, nil, nil, false, err
	}
	if _, err := run.Discover(ctx, src.ID); err != nil {
		return nil, nil, nil, nil, false, err
	}
	return run, q, m, src, optionsKept, nil
}

// handlerCheck answers render checks by serving a GET through h.
func handlerCheck(h http.Handler) func(context.Context, string) int {
	return func(ctx context.Context, p string) int {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, (&url.URL{Path: p}).EscapedPath(), nil)
		if err != nil {
			return 0
		}
		req.RemoteAddr = "127.0.0.1:0"
		w := &statusWriter{header: http.Header{}}
		h.ServeHTTP(w, req)
		if w.status == 0 {
			w.status = http.StatusOK
		}
		return w.status
	}
}

// statusWriter keeps only the response status (the body is discarded).
type statusWriter struct {
	header http.Header
	status int
}

func (w *statusWriter) Header() http.Header { return w.header }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(b), nil
}
func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// registered returns the module's service when the module is booted.
func registered(deps *kernel.Deps) (*service, bool) {
	v, ok := kernel.Get[Service](deps, ServiceName)
	if !ok {
		return nil, false
	}
	s, ok := v.(*service)
	return s, ok
}
