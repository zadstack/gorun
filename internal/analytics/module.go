// Package analytics is GoRun's built-in, cookieless site analytics: a page
// view and form-submit beacon, stored in its own analytics.db beside the
// main database, with daily rollups and an admin dashboard. Visitors are
// counted by a daily-salted hash; the raw IP, the user agent and query
// strings are never stored (docs/superpowers/specs/2026-11-03-site-analytics-design.md).
package analytics

import (
	"context"
	"database/sql"
	"io/fs"
	"time"

	"github.com/zadstack/gorun/internal/kernel"
)

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
}

// New returns the analytics module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return ModuleName }

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
// when the app closes), declares analytics.view and adds the hourly rollup
// cron. A store that fails to open is logged and the module registers
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
	d.OnClose(func() {
		stop()
		<-done // Run flushes what is queued before returning
		if err := store.Close(); err != nil {
			d.Log.Error("analytics: close analytics.db", "err", err)
		}
	})

	d.Perms.Declare(PermView, map[string]string{"en": "View analytics", "ar": "عرض الإحصائيات"})
	d.Jobs.Cron(CronRollup, rollupEvery, m.rollup)
	return nil
}

// rollup is the analytics.rollup cron: it recomputes daily for today and
// yesterday and backfills an empty daily (Task 3).
func (m *Module) rollup(context.Context) error { return nil }
