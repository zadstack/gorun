package settings

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "settings", Migrations()); err != nil {
		t.Fatal(err)
	}
	return New(d)
}

type siteInfo struct {
	Title  string `json:"title"`
	Locale string `json:"locale"`
}

func TestSetGetRoundtripStruct(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	in := siteInfo{Title: "زاد ستاك", Locale: "ar"}
	if err := s.Set(ctx, "site.info", in); err != nil {
		t.Fatal(err)
	}
	var out siteInfo
	if err := s.Get(ctx, "site.info", &out); err != nil || out != in {
		t.Fatalf("got %+v %v", out, err)
	}
	if err := s.Set(ctx, "site.info", siteInfo{Title: "new"}); err != nil {
		t.Fatal(err)
	}
	s.Get(ctx, "site.info", &out)
	if out.Title != "new" {
		t.Fatalf("overwrite failed: %+v", out)
	}
}

func TestGetNotFound(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	var v string
	if err := s.Get(ctx, "nope", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound got %v", err)
	}
	if got := s.GetString(ctx, "nope", "def"); got != "def" {
		t.Fatal(got)
	}
	s.Set(ctx, "site.title", "GoRun")
	if got := s.GetString(ctx, "site.title", "def"); got != "GoRun" {
		t.Fatal(got)
	}
}

func TestAllByPrefix(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	s.Set(ctx, "site.title", "T")
	s.Set(ctx, "site.locale", "ar")
	s.Set(ctx, "siteother", 1)
	s.Set(ctx, "seo.title", "x")
	m, err := s.All(ctx, "site.")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || string(m["site.locale"]) != `"ar"` {
		t.Fatalf("got %v", m)
	}
	all, _ := s.All(ctx, "")
	if len(all) != 4 {
		t.Fatalf("want 4 got %d", len(all))
	}
}

func TestGetStringLogsDBErrorsButNotMissingKeys(t *testing.T) {
	var buf bytes.Buffer
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d, "settings", Migrations()); err != nil {
		t.Fatal(err)
	}
	s := New(d).WithLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	if got := s.GetString(context.Background(), "missing", "def"); got != "def" || buf.Len() != 0 {
		t.Fatalf("missing key: got %q, log %q", got, buf.String())
	}
	d.Close() // every query now fails
	if got := s.GetString(context.Background(), "site.title", "def"); got != "def" {
		t.Fatal(got)
	}
	if !strings.Contains(buf.String(), "site.title") {
		t.Fatalf("DB error not logged: %q", buf.String())
	}
}

func TestGetUsesProcessCacheAndInvalidatesOnWrite(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	if err := s.Set(ctx, "site.title", "GoRun"); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "GoRun" {
		t.Fatalf("first get: %q %v", v, err)
	}
	// Corrupt the row under the store so a cache miss would fail or return wrong data.
	if _, err := s.db.ExecContext(ctx, `UPDATE settings SET value_json = '"stale"' WHERE key = 'site.title'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "GoRun" {
		t.Fatalf("cached get must not hit SQLite: %q %v", v, err)
	}
	if err := s.Set(ctx, "site.title", "Next"); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "Next" {
		t.Fatalf("after Set: %q %v", v, err)
	}
	if err := s.Delete(ctx, "site.title"); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.title", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: %v", err)
	}
	// Negative cache: insert under the store and ensure Get still sees not-found
	// until Set refreshes — wait, after Delete we forget, so next Get queries DB.
	// Fresh miss then remembers not-found:
	if err := s.Get(ctx, "site.missing", &v); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value_json, updated_at) VALUES ('site.missing', '"x"', ?)`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.missing", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative cache should hide bypass insert until invalidate: %v %q", err, v)
	}
	if err := s.Set(ctx, "site.missing", "y"); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.missing", &v); err != nil || v != "y" {
		t.Fatalf("Set must clear negative cache: %q %v", v, err)
	}
}

func TestGetDoesNotRepopulateAfterConcurrentSet(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	if err := s.Set(ctx, "site.title", "A"); err != nil {
		t.Fatal(err)
	}
	gen := s.cache.generation
	if err := s.Set(ctx, "site.title", "B"); err != nil {
		t.Fatal(err)
	}
	s.remember("site.title", `"A"`, true, gen) // stale fill from before Set
	var v string
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "B" {
		t.Fatalf("stale remember must not win: %q %v", v, err)
	}
}

func TestSetTxWriteThroughVisibleAfterCommit(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	_ = s.Set(ctx, "site.title", "A")
	var v string
	_ = s.Get(ctx, "site.title", &v)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTx(ctx, tx, "site.title", "B"); err != nil {
		t.Fatal(err)
	}
	// Write-through makes B visible to Get before commit (avoids re-caching A
	// from the pool connection that cannot see the open transaction).
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "B" {
		_ = tx.Rollback()
		t.Fatalf("SetTx must publish: %q %v", v, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "site.title", &v); err != nil || v != "B" {
		t.Fatalf("after commit: %q %v", v, err)
	}
}

func TestWithLoggerSharesCache(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	_ = s.Set(ctx, "site.title", "A")
	var v string
	_ = s.Get(ctx, "site.title", &v)
	other := s.WithLogger(slog.Default())
	if _, err := s.db.ExecContext(ctx, `UPDATE settings SET value_json = '"B"' WHERE key = 'site.title'`); err != nil {
		t.Fatal(err)
	}
	if err := other.Get(ctx, "site.title", &v); err != nil || v != "A" {
		t.Fatalf("WithLogger must share cache: %q %v", v, err)
	}
}

func TestDeleteAndDeletePrefix(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"ext.a.settings.x", "ext.a.state", "ext.ab.settings.x", "site.title"} {
		if err := s.Set(ctx, k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeletePrefix(ctx, "ext.a."); err != nil {
		t.Fatal(err)
	}
	all, _ := s.All(ctx, "")
	if len(all) != 2 || all["ext.ab.settings.x"] == nil || all["site.title"] == nil {
		t.Fatalf("DeletePrefix must remove only keys under the prefix: %v", all)
	}
	if err := s.DeletePrefix(ctx, ""); err == nil {
		t.Fatal("an empty prefix would wipe every setting; it must be refused")
	}
	if err := s.Delete(ctx, "site.title"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "site.title"); err != nil {
		t.Fatalf("deleting a missing key is not an error: %v", err)
	}
	var v string
	if err := s.Get(ctx, "site.title", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key: %v", err)
	}

	// Tx variant: rolled back with the transaction, applied on commit.
	_ = s.Set(ctx, "ext.b.k", "v")
	tx, _ := s.db.BeginTx(ctx, nil)
	if err := s.DeletePrefixTx(ctx, tx, "ext.b."); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if err := s.Get(ctx, "ext.b.k", &v); err != nil {
		t.Fatalf("rollback must keep the key: %v", err)
	}
	tx, _ = s.db.BeginTx(ctx, nil)
	if err := s.DeletePrefixTx(ctx, tx, "ext.b."); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, "ext.b.k", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("committed DeletePrefixTx: %v", err)
	}
}
