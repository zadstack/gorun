package diagnostics

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func testCollector(t *testing.T) *Collector {
	t.Helper()
	d, e := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(context.Background(), d, "diagnostics", Migrations()); e != nil {
		t.Fatal(e)
	}
	c := New(d)
	t.Cleanup(func() { c.Close(); d.Close() })
	return c
}
func TestCollectorPersistsOnlySafeClassificationAndFlushesShutdown(t *testing.T) {
	c := testCollector(t)
	c.Start()
	l := slog.New(c.Handler(slog.NewTextHandler(io.Discard, nil)))
	l.Error("admin: secret body password=hidden", "token", "re_super_secret", "err", "child private report", "path", "/auth/magic/secret")
	l.Info("http", "status", 503, "path", "/customer-secret?q=private")
	l.Info("http", "status", 404)
	c.Close()
	rows, e := c.List(context.Background(), Filter{})
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 {
		t.Fatal("rows", rows)
	}
	for _, r := range rows {
		if strings.Contains(r.Code, "secret") || strings.Contains(r.Category, "secret") {
			t.Fatal("leak")
		}
	}
	var schema, raw string
	if e = c.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='diagnostic_events'`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(schema, "message") || strings.Contains(schema, "payload") {
		t.Fatal("unsafe schema")
	}
	if e = c.db.QueryRow(`SELECT group_concat(category||code) FROM diagnostic_events`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, "hidden") || strings.Contains(raw, "private") {
		t.Fatal("persisted secret")
	}
}
func TestCollectorOverflowIsBoundedAndRecordsDropped(t *testing.T) {
	c := testCollector(t)
	for i := 0; i < queueSize+20; i++ {
		c.observe("application", "runtime_error", 0, time.Now())
	}
	if c.Dropped() != 20 {
		t.Fatal("drop", c.Dropped())
	}
	c.Start()
	c.Close()
	rows, e := c.List(context.Background(), Filter{})
	if e != nil || len(rows) != 1 || rows[0].Count != queueSize {
		t.Fatal(rows, e)
	}
}
func TestCollectorLoggingInsideWriteTransactionDoesNotBlock(t *testing.T) {
	c := testCollector(t)
	c.Start()
	tx, e := c.db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		slog.New(c.Handler(slog.NewTextHandler(io.Discard, nil))).Error("database failure", "err", "private")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("logger blocked on database")
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	c.Close()
}
func TestCollectorRetentionAndCapacity(t *testing.T) {
	c := testCollector(t)
	now := time.Now().UTC()
	for i := 0; i < maxRows+5; i++ {
		_, e := c.db.Exec(`INSERT INTO diagnostic_events(day,category,code,http_status,first_at,last_at,count) VALUES(?, 'application','runtime_error',0,?,?,1)`, time.Unix(int64(i), 0).Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e := c.db.Exec(`INSERT INTO diagnostic_events VALUES('old','http','http_5xx',500,'2000-01-01T00:00:00Z','2000-01-01T00:00:00Z',1)`)
	if e != nil {
		t.Fatal(e)
	}
	c.observe("http", "http_5xx", 500, now)
	c.Start()
	c.Close()
	var n int
	if e = c.db.QueryRow(`SELECT count(*) FROM diagnostic_events`).Scan(&n); e != nil || n > maxRows {
		t.Fatal(n, e)
	}
	if e = c.db.QueryRow(`SELECT count(*) FROM diagnostic_events WHERE day='old'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("retention", n, e)
	}
}
func TestCollectorPreservesWithAttrsAndGroupsWithoutRetainingThem(t *testing.T) {
	c := testCollector(t)
	c.Start()
	slog.New(c.Handler(slog.NewTextHandler(io.Discard, nil))).With("secret", "hidden").WithGroup("private").Error("unknown secret error")
	c.Close()
	rows, e := c.List(context.Background(), Filter{})
	if e != nil || len(rows) != 1 || rows[0].Code != "runtime_error" {
		t.Fatal(rows, e)
	}
}

func TestCollectorStorageFailureDoesNotRecurseOrBlockShutdown(t *testing.T) {
	c := testCollector(t)
	c.observe("application", "runtime_error", 0, time.Now())
	if e := c.db.Close(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown blocked")
	}
	if c.StorageFailures() == 0 || c.Dropped() != 1 {
		t.Fatal("missing failure accounting", c.StorageFailures(), c.Dropped())
	}
	c.observe("application", "runtime_error", 0, time.Now())
	c.Close()
}

func TestCollectorHTTPIndependentFromConsoleLogLevel(t *testing.T) {
	c := testCollector(t)
	l := slog.New(c.Handler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	l.Info("http", "status", 502, "path", "private")
	c.Close()
	rows, e := c.List(context.Background(), Filter{})
	if e != nil || len(rows) != 1 || rows[0].HTTPStatus != 502 {
		t.Fatalf("missing HTTP classification: %#v %v", rows, e)
	}
}
