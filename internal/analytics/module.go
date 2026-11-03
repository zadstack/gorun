// Package analytics is GoRun's built-in, cookieless site analytics: a page
// view and form-submit beacon, stored in its own analytics.db beside the
// main database, with daily rollups and an admin dashboard. Visitors are
// counted by a daily-salted hash; the raw IP, the user agent and query
// strings are never stored (docs/superpowers/specs/2026-11-03-site-analytics-design.md).
package analytics

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"time"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

//go:embed catalog/*.json
var catalogs embed.FS

// Module name, permission and cron.
const (
	ModuleName = "analytics"
	PermView   = "analytics.view"
	CronRollup = "analytics.rollup"
)

// rollupEvery is the analytics.rollup cron interval.
const rollupEvery = time.Hour

// Module is the analytics kernel.Module.
type Module struct {
	store *Store  // nil when analytics.db could not be opened: collection is off
	buf   *Buffer // nil with store

	// Collection (collect.go), set in Register.
	settings *settings.Store
	log      *slog.Logger
	geo      geo
	limiter  *limiter
	siteID   string // siteIDFor(secret key)
	origin   string // configured site origin (scheme://host)
	siteHost string // configured site host name

	seen *seenIDs // recent form submission ids (forms.go)
}

// New returns the analytics module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return ModuleName }

// Catalogs implements kernel.CatalogProvider (the analytics.* strings).
func (*Module) Catalogs() fs.FS { return catalogs }

// Migrations implements kernel.Module. The module has no tables in the main
// database: its migrations (migrations/) belong to analytics.db and
// OpenStore applies them there.
func (*Module) Migrations() fs.FS { return nil }

// Enabled implements kernel.OptionalModule: on unless the operator turned it
// off (addons.analytics in the config or the addons.analytics.enabled
// setting). Disabled, it opens no analytics.db and registers nothing; an
// existing file is kept.
func (*Module) Enabled(ctx context.Context, d *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, d, ModuleName)
}

// Register opens analytics.db, starts the write buffer (stopped and flushed
// when the app closes), serves the beacon script and endpoint, injects the
// script into public HTML, records form submissions (site.form.submitted),
// declares analytics.view, adds the hourly rollup cron and registers the
// admin pages, the dashboard widget and the summary API operation. A store that fails to open is logged and the module registers
// nothing else, so the site keeps serving with collection off.
func (m *Module) Register(d *kernel.Deps) error {
	store, err := OpenStore(d.Config.DataDir)
	if err != nil {
		d.Log.Error("analytics: analytics.db unavailable; collection is off", "err", err)
		return nil
	}
	m.store = store
	m.buf = NewBuffer(store, BufferCap, FlushEvery, FlushBatch, d.Log)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.buf.Run(ctx)
	}()
	m.settings, m.log = d.Settings, d.Log
	m.limiter = newLimiter(rateLimit, rateWindow)
	m.siteID = siteIDFor(d.Config.SecretKey)
	m.origin, m.siteHost = siteOrigin(d.Config.BaseURL)
	g, closeGeo := openGeo(d.Config.DataDir, d.Log)
	m.geo = g
	d.OnClose(func() {
		stop()
		<-done // Run flushes what is queued before returning
		if err := store.Close(); err != nil {
			d.Log.Error("analytics: close analytics.db", "err", err)
		}
		closeGeo()
	})

	pub := d.Router.Public
	pub.Get(ScriptPath, serveScript)
	pub.Head(ScriptPath, serveScript)
	pub.Post(BeaconPath, m.beacon)
	pub.Use(m.inject)

	m.seen = newSeenIDs(seenSubmissions)
	d.Hooks.Action(hookFormSubmitted, 50, m.formSubmitted)

	d.Perms.Declare(PermView, map[string]string{"en": "View analytics", "ar": "عرض الإحصائيات"})
	d.Jobs.Cron(CronRollup, rollupEvery, m.rollup)
	m.registerAdmin(d)
	m.registerAPI(d)
	return nil
}

// rollup is the analytics.rollup cron body (Store.Rollup). The cron's
// first run happens on the scheduler's first tick after boot, which is
// what backfills daily on first start.
func (m *Module) rollup(ctx context.Context) error { return m.store.Rollup(ctx, time.Now()) }
