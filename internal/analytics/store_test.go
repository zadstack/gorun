package analytics

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func countEvents(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.With(context.Background(), func(d *sql.DB) error {
		return d.QueryRow(`SELECT count(*) FROM events`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenStoreMigrates(t *testing.T) {
	s, dir := openTestStore(t)
	if _, err := os.Stat(filepath.Join(dir, DBFile)); err != nil {
		t.Fatalf("analytics.db not created: %v", err)
	}
	for _, table := range []string{"events", "daily", "salts"} {
		var n int
		if err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	// Opening again is idempotent (migrations are recorded).
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()
}

func TestInsertBatch(t *testing.T) {
	s, _ := openTestStore(t)
	txs := 0
	s.onTx = func() { txs++ }
	evs := make([]Event, 1200)
	ts := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for i := range evs {
		evs[i] = Event{TS: ts, Kind: KindView, Path: "/p"}
	}
	if err := s.Insert(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, s); n != 1200 {
		t.Fatalf("stored %d events, want 1200", n)
	}
	if txs == 0 || txs > 3 {
		t.Fatalf("%d transactions, want 1..3", txs)
	}
}

func TestInsertStoresAllFields(t *testing.T) {
	s, _ := openTestStore(t)
	e := Event{TS: time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC), Kind: KindSubmit, Path: "/contact", Visitor: "abcd", RefHost: "example.com",
		UTMSource: "news", UTMMedium: "email", UTMCampaign: "launch", Browser: "Firefox", OS: "Linux", Device: "desktop",
		Country: "AE", Locale: "ar", Form: "contact"}
	if err := s.Insert(context.Background(), []Event{e}); err != nil {
		t.Fatal(err)
	}
	var got Event
	var ts string
	err := s.DB().QueryRow(`SELECT ts, kind, path, visitor, ref_host, utm_source, utm_medium, utm_campaign, browser, os, device, country, locale, form FROM events`).
		Scan(&ts, &got.Kind, &got.Path, &got.Visitor, &got.RefHost, &got.UTMSource, &got.UTMMedium, &got.UTMCampaign, &got.Browser, &got.OS, &got.Device, &got.Country, &got.Locale, &got.Form)
	if err != nil {
		t.Fatal(err)
	}
	if ts != "2026-11-03T08:30:00Z" {
		t.Errorf("ts = %q", ts)
	}
	got.TS = e.TS
	if got != e {
		t.Errorf("got %+v, want %+v", got, e)
	}
}

func TestFlushKeepsEventDay(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 11, 2, 23, 59, 59, 0, time.UTC)
	visitor, err := s.VisitorHash(ctx, late, "site", "203.0.113.7", "UA")
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuffer(s, 10, time.Hour, 500, nil)
	b.now = func() time.Time { return day.Add(48 * time.Hour) } // the flush happens days later
	if !b.Add(Event{TS: late, Kind: KindView, Path: "/", Visitor: visitor}) {
		t.Fatal("event dropped")
	}
	// Midnight passes and the next day's salt rotates in before the flush.
	if _, err := s.VisitorHash(ctx, late.Add(time.Second), "site", "203.0.113.7", "UA"); err != nil {
		t.Fatal(err)
	}
	if err := b.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var ts, v string
	if err := s.DB().QueryRow(`SELECT ts, visitor FROM events`).Scan(&ts, &v); err != nil {
		t.Fatal(err)
	}
	if ts != "2026-11-02T23:59:59Z" {
		t.Errorf("ts = %q, want 2026-11-02T23:59:59Z", ts)
	}
	if v != visitor {
		t.Errorf("visitor = %q, want the hash made with the event's day salt %q", v, visitor)
	}
}

func TestStoreRecreatesMissingFile(t *testing.T) {
	s, dir := openTestStore(t)
	ctx := context.Background()
	ev := []Event{{TS: time.Now(), Kind: KindView, Path: "/"}}
	if err := s.Insert(ctx, ev); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DBFile)
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if n := countEvents(t, s); n != 0 {
		t.Fatalf("reads after file loss see %d events of the deleted file, want an empty database", n)
	}
	if err := s.Insert(ctx, ev); err != nil {
		t.Fatalf("insert after file loss: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("analytics.db not recreated: %v", err)
	}
	if n := countEvents(t, s); n != 1 {
		t.Fatalf("recreated file holds %d events, want 1", n)
	}
	if _, err := s.VisitorHash(ctx, time.Now(), "site", "ip", "ua"); err != nil {
		t.Fatalf("visitor hash after recreate: %v", err)
	}
}

func bootApp(t *testing.T, addons map[string]string) (*kernel.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://site.test", Theme: "default", Addons: addons}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app, dir
}

func TestModuleDisabled(t *testing.T) {
	app, dir := bootApp(t, map[string]string{"analytics": "disabled"})
	if _, err := os.Stat(filepath.Join(dir, DBFile)); !os.IsNotExist(err) {
		t.Fatalf("analytics.db exists while disabled (stat err %v)", err)
	}
	if slices.Contains(app.Deps.Jobs.CronNames(), CronRollup) {
		t.Error("rollup cron registered while disabled")
	}
	srv := httptest.NewServer(app.Handler)
	defer srv.Close()
	// TestModuleEnabledServesScript is the enabled counterpart (200).
	resp, err := http.Get(srv.URL + "/_a/s.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/_a/s.js = %d, want 404", resp.StatusCode)
	}
}

func TestModuleEnabledOpensStoreAndRegisters(t *testing.T) {
	app, dir := bootApp(t, nil)
	if _, err := os.Stat(filepath.Join(dir, DBFile)); err != nil {
		t.Fatalf("analytics.db not created: %v", err)
	}
	if !slices.Contains(app.Deps.Jobs.CronNames(), CronRollup) {
		t.Errorf("cron %s not registered: %v", CronRollup, app.Deps.Jobs.CronNames())
	}
	i := slices.IndexFunc(app.Deps.Perms.All(), func(p kernel.Permission) bool { return p.Name == PermView })
	if i < 0 {
		t.Fatalf("permission %s not declared", PermView)
	}
	if l := app.Deps.Perms.All()[i].Label; l["en"] != "View analytics" || l["ar"] != "عرض الإحصائيات" {
		t.Errorf("labels = %v", l)
	}
}

func TestModuleCloseFlushesBuffer(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"), BaseURL: "http://site.test", Theme: "default"}
	mod := New()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	if !mod.buf.Add(Event{TS: time.Now(), Kind: KindView, Path: "/"}) {
		t.Fatal("dropped")
	}
	app.Close()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := countEvents(t, s); n != 1 {
		t.Fatalf("after close: %d events, want 1", n)
	}
}

func TestModuleStoreFailureLeavesSiteServing(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be makes the open fail.
	if err := os.MkdirAll(filepath.Join(dir, DBFile), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"), BaseURL: "http://site.test", Theme: "default"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), New()})
	if err != nil {
		t.Fatalf("boot failed: %v", err)
	}
	defer app.Close()
	if slices.Contains(app.Deps.Jobs.CronNames(), CronRollup) {
		t.Error("rollup cron registered without a store")
	}
}

// An insert holding a connection of the old handle while the file is
// deleted must finish before the reopen closes that handle: otherwise the
// late close of the in-use connection unlinks the new database's -wal by
// path and rows committed to the new file vanish.
func TestReopenWaitsForInFlightInsert(t *testing.T) {
	s, dir := openTestStore(t)
	ctx := context.Background()
	path := filepath.Join(dir, DBFile)
	inside, release := make(chan struct{}), make(chan struct{})
	var once bool
	s.inTx = func() error {
		if !once {
			once = true
			close(inside)
			<-release // the transaction stays open, its connection in use
		}
		return nil
	}
	insertDone := make(chan error, 1)
	go func() { insertDone <- s.Insert(ctx, []Event{{TS: time.Now(), Kind: KindView, Path: "/old"}}) }()
	<-inside
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
	reopened := make(chan error, 1)
	go func() { reopened <- s.Reopen() }()
	select {
	case err := <-reopened:
		t.Fatalf("Reopen returned (%v) while an insert held the old handle", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-insertDone; err != nil {
		t.Fatalf("in-flight insert: %v", err)
	}
	if err := <-reopened; err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s.inTx = nil

	evs := make([]Event, 50)
	for i := range evs {
		evs[i] = Event{TS: time.Now(), Kind: KindView, Path: "/new"}
	}
	if err := s.Insert(ctx, evs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("new database's WAL missing after reopen: %v", err)
	}
	var check string
	if err := s.With(ctx, func(d *sql.DB) error { return d.QueryRow(`PRAGMA integrity_check`).Scan(&check) }); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
	// A second process (another Store) sees the rows: they reached the new
	// file and its WAL, not an unlinked one.
	other, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if n := countEvents(t, other); n != 50 {
		t.Fatalf("new database holds %d events, want 50", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, other); n != 50 {
		t.Fatalf("after closing the writer: %d events, want 50", n)
	}
}

func TestStoreClosedRefusesUse(t *testing.T) {
	s, dir := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(context.Background(), []Event{{Kind: KindView, Path: "/"}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Insert after Close = %v, want ErrClosed", err)
	}
	if err := s.With(context.Background(), func(*sql.DB) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("With after Close = %v, want ErrClosed", err)
	}
	if s.DB() == nil {
		t.Fatal("DB() returned nil")
	}
	_ = os.Remove(filepath.Join(dir, DBFile))
	if err := s.Reopen(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Reopen after Close = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFile)); !os.IsNotExist(err) {
		t.Fatal("a closed store recreated its file")
	}
}
