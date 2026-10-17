package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

// HealthCheckFilter is the hooks filter other modules use to contribute
// checks to System Health. The payload is *[]admin.HealthCheck; append to
// it and return it.
const HealthCheckFilter = "system.health.checks"

// Health check statuses.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
)

// HealthCheck is one line of the System Health "Checks" list.
type HealthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

const (
	failedJobsShown = 20
	lowDiskRatio    = 0.10
)

// healthReport is the System Health page and the /api/v1/system/health
// payload. It never contains secrets.
type healthReport struct {
	GoVersion     string        `json:"go_version"`
	OS            string        `json:"os"`
	Arch          string        `json:"arch"`
	NumCPU        int           `json:"num_cpu"`
	Goroutines    int           `json:"goroutines"`
	HeapAlloc     string        `json:"heap_alloc"`
	DBPath        string        `json:"db_path"`
	DBSize        string        `json:"db_size"`
	WALSize       string        `json:"wal_size"`
	JournalMode   string        `json:"journal_mode"`
	DiskFree      string        `json:"disk_free"`
	DiskTotal     string        `json:"disk_total"`
	Jobs          jobs.Stats    `json:"jobs"`
	JobRows       int           `json:"job_rows"`
	FailedJobs    []jobs.Job    `json:"failed_jobs"`
	Hooks         []string      `json:"hooks"`
	Modules       []string      `json:"modules"`
	LastMigration string        `json:"last_migration"`
	Theme         string        `json:"theme"`
	Uptime        string        `json:"uptime"`
	StartedAt     string        `json:"started_at"`
	Env           string        `json:"env"`
	BaseURL       string        `json:"base_url"`
	DataDir       string        `json:"data_dir"`
	AllowPrivate  bool          `json:"allow_private_net"`
	Checks        []HealthCheck `json:"checks"`
}

// healthReport gathers runtime, database, disk, jobs and module state.
func (h *handlers) healthReport(ctx context.Context) (*healthReport, error) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	cfg := h.d.Config
	rep := &healthReport{GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, NumCPU: runtime.NumCPU(),
		Goroutines: runtime.NumGoroutine(), HeapAlloc: humanBytes(mem.HeapAlloc), DBPath: cfg.DBPath(),
		DBSize: fileSize(cfg.DBPath()), WALSize: fileSize(cfg.DBPath() + "-wal"), Hooks: h.d.Hooks.Names(),
		Uptime: time.Since(h.started).Truncate(time.Second).String(), StartedAt: h.started.UTC().Format(time.RFC3339),
		Env: cfg.Env, BaseURL: kernel.BaseURL(ctx, h.d), DataDir: cfg.DataDir, AllowPrivate: cfg.AllowPrivateNet, DiskFree: "—", DiskTotal: "—"}
	if rep.Hooks == nil {
		rep.Hooks = []string{}
	}
	if err := h.d.DB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&rep.JournalMode); err != nil {
		return nil, fmt.Errorf("admin: journal mode: %w", err)
	}
	free, total, diskOK := diskUsage(cfg.DataDir)
	if diskOK {
		rep.DiskFree, rep.DiskTotal = humanBytes(free), humanBytes(total)
	}
	var err error
	if rep.Jobs, err = h.d.Jobs.Stats(ctx); err != nil {
		return nil, err
	}
	if rep.JobRows, err = h.d.Jobs.Count(ctx); err != nil {
		return nil, err
	}
	if rep.FailedJobs, err = failedJobs(ctx, h.d.DB, failedJobsShown); err != nil {
		return nil, err
	}
	if rep.Modules, rep.LastMigration, err = h.modules(ctx); err != nil {
		return nil, err
	}
	rep.Theme = h.activeTheme(ctx)
	rep.Checks = h.checks(ctx, rep, free, total, diskOK)
	return rep, nil
}

// modules lists BootOrder modules that are present: they applied
// migrations, published a service under their name, or are always booted.
// The kernel does not expose the booted module list yet.
func (h *handlers) modules(ctx context.Context) ([]string, string, error) {
	rows, err := h.d.DB.QueryContext(ctx, `SELECT DISTINCT module FROM schema_migrations`)
	if err != nil {
		return nil, "", fmt.Errorf("admin: modules: %w", err)
	}
	migrated := map[string]bool{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			return nil, "", err
		}
		migrated[m] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	always := map[string]bool{"db": true, "i18n": true, "admin": true}
	var out []string
	for _, m := range kernel.BootOrder {
		if _, svc := kernel.Get[any](h.d, m); always[m] || migrated[m] || svc {
			out = append(out, m)
		}
	}
	var last, at string
	err = h.d.DB.QueryRowContext(ctx, `SELECT module || '/' || name, applied_at FROM schema_migrations ORDER BY applied_at DESC, rowid DESC LIMIT 1`).Scan(&last, &at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("admin: last migration: %w", err)
	}
	if last != "" {
		last += " (" + at + ")"
	}
	return out, last, nil
}

func (h *handlers) activeTheme(ctx context.Context) string {
	if ts, ok := h.themeService(); ok {
		return ts.Active(ctx).Slug
	}
	return h.d.Settings.GetString(ctx, "site.theme", h.d.Config.Theme)
}

// checks builds the base checks, then lets modules add theirs through the
// HealthCheckFilter hook.
func (h *handlers) checks(ctx context.Context, rep *healthReport, free, total uint64, diskOK bool) []HealthCheck {
	list := []HealthCheck{{Name: "database", Status: CheckOK, Detail: "journal_mode=" + rep.JournalMode}}
	if err := h.d.DB.PingContext(ctx); err != nil {
		list[0] = HealthCheck{Name: "database", Status: CheckFail, Detail: err.Error()}
	} else if rep.JournalMode != "wal" {
		list[0].Status = CheckWarn
	}
	tr := func(key string, args ...any) string { return h.d.I18n.T(i18n.Locale(ctx), key, args...) }
	jc := HealthCheck{Name: "jobs", Status: CheckOK, Detail: tr("admin.health.detail_jobs", rep.Jobs.Queued, rep.Jobs.Running, rep.Jobs.Failed, rep.Jobs.Dead)}
	if rep.Jobs.Dead > 0 || rep.Jobs.Failed > 0 {
		jc.Status = CheckWarn
	}
	list = append(list, jc)
	switch {
	case !diskOK:
		list = append(list, HealthCheck{Name: "disk", Status: CheckWarn, Detail: tr("admin.health.detail_disk_unavailable", runtime.GOOS)})
	case total > 0 && float64(free)/float64(total) < lowDiskRatio:
		list = append(list, HealthCheck{Name: "disk", Status: CheckWarn, Detail: tr("admin.health.detail_disk_free", humanBytes(free))})
	default:
		list = append(list, HealthCheck{Name: "disk", Status: CheckOK, Detail: tr("admin.health.detail_disk_free", humanBytes(free))})
	}
	mc := HealthCheck{Name: "mail", Status: CheckWarn, Detail: tr("admin.health.detail_mail_unset")}
	if host := h.d.Settings.GetString(ctx, "mail.host", ""); host != "" {
		mc = HealthCheck{Name: "mail", Status: CheckOK, Detail: tr("admin.health.detail_mail_ok", host)}
	}
	list = append(list, mc)
	list = append(list, h.schemaCheck(ctx, tr))
	out, err := h.d.Hooks.Apply(ctx, HealthCheckFilter, &list)
	if err != nil {
		list = append(list, HealthCheck{Name: HealthCheckFilter, Status: CheckFail, Detail: err.Error()})
	} else if p, ok := out.(*[]HealthCheck); ok && p != nil {
		list = *p
	}
	for i, c := range list {
		if !slices.Contains([]string{CheckOK, CheckWarn, CheckFail}, c.Status) {
			list[i].Status = CheckWarn
		}
	}
	return list
}

// schemaMigrationsShown caps how many migration names the schema check lists.
const schemaMigrationsShown = 10

// schemaCheck compares schema_migrations with this binary's migrations: warn
// when the database is ahead (a newer binary ran) or an applied file changed.
func (h *handlers) schemaCheck(ctx context.Context, tr func(string, ...any) string) HealthCheck {
	inspect, ok := kernel.Get[kernel.SchemaInspector](h.d, kernel.ServiceSchema)
	if !ok {
		return HealthCheck{Name: "schema", Status: CheckWarn, Detail: tr("admin.health.detail_schema_unavailable")}
	}
	got, err := inspect(ctx)
	switch {
	case err != nil:
		return HealthCheck{Name: "schema", Status: CheckWarn, Detail: err.Error()}
	case got.Ahead():
		return HealthCheck{Name: "schema", Status: CheckWarn, Detail: tr("admin.health.detail_schema_ahead", len(got.Unknown), shortList(got.Unknown))}
	case len(got.Mismatched) > 0:
		return HealthCheck{Name: "schema", Status: CheckWarn, Detail: tr("admin.health.detail_schema_changed", len(got.Mismatched), shortList(got.Mismatched))}
	}
	return HealthCheck{Name: "schema", Status: CheckOK, Detail: tr("admin.health.detail_schema_ok")}
}

func shortList(names []string) string {
	if len(names) <= schemaMigrationsShown {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:schemaMigrationsShown], ", ") + ", …"
}

func fileSize(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "—"
	}
	return humanBytes(uint64(st.Size())) //nolint:gosec // file sizes are non-negative
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	rep, err := h.healthReport(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/system/health.html", rep)
}
