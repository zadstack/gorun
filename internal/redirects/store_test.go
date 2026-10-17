package redirects

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
)

func testService(t *testing.T) *service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d, "redirects", migrations); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newService(d, hooks.New(), audit.New(d, log), log)
	t.Cleanup(func() { _ = s.flushHits(context.Background()) })
	return s
}

func TestRedirectLookupExactAndHit(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	r, err := s.Save(ctx, Redirect{FromPath: "/Old-Page/", ToURL: "/new-page", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.FromPath != "/Old-Page" || r.Status != 301 || r.CreatedAt == "" {
		t.Fatalf("saved: %+v", r)
	}
	for _, p := range []string{"/Old-Page", "/Old-Page/", "Old-Page"} {
		if got, ok := s.Lookup(ctx, p); !ok || got.ID != r.ID {
			t.Errorf("lookup %q: %v", p, ok)
		}
	}
	for _, p := range []string{"/old-page", "/Old-Page/x", "/Old", "/"} {
		if _, ok := s.Lookup(ctx, p); ok {
			t.Errorf("lookup %q must not match (exact, case-sensitive)", p)
		}
	}
	// Percent-encoded sources match decoded request paths (Arabic slugs).
	if _, err := s.Save(ctx, Redirect{FromPath: "/%D9%85%D9%82%D8%A7%D9%84", ToURL: "https://example.com/a?b=1", Status: 308, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Lookup(ctx, "/مقال"); !ok || got.Status != 308 {
		t.Fatalf("encoded lookup: %v %+v", ok, got)
	}
	// Disabled redirects never match; changes invalidate the cache.
	r.Enabled = false
	if _, err := s.Save(ctx, *r); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(ctx, "/Old-Page"); ok {
		t.Fatal("disabled redirect matched")
	}
	r.Enabled = true
	r.Hits = 999 // ignored
	if _, err := s.Save(ctx, *r); err != nil {
		t.Fatal(err)
	}
	// Hits are buffered, then written in one batch.
	for range 3 {
		s.Hit(ctx, r.ID)
	}
	if got, _ := s.get(ctx, r.ID); got.Hits != 0 {
		t.Fatalf("hits written synchronously: %d", got.Hits)
	}
	if err := s.flushHits(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.get(ctx, r.ID); got.Hits != 3 {
		t.Fatalf("hits after flush: %d", got.Hits)
	}
	// Validation.
	var ve *ValidationError
	cases := []Redirect{
		{FromPath: "", ToURL: "/x"},
		{FromPath: "/a", ToURL: "javascript:alert(1)"},
		{FromPath: "/a", ToURL: "//evil.example"},
		{FromPath: "/a", ToURL: "/a/"},
		{FromPath: "/a", ToURL: "/b", Status: 200},
		{FromPath: "https://x.example/a", ToURL: "/b"},
		{FromPath: "/Old-Page", ToURL: "/b"}, // taken
	}
	for _, c := range cases {
		if _, err := s.Save(ctx, c); !errors.As(err, &ve) {
			t.Errorf("%+v accepted: %v", c, err)
		}
	}
	if _, err := s.Save(ctx, Redirect{ID: "missing", FromPath: "/z", ToURL: "/y"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	list, total, err := s.List(ctx, ListQuery{Search: "example.com"})
	if err != nil || total != 1 || !strings.HasPrefix(list[0].ToURL, "https://example.com") {
		t.Fatalf("search: %v %d", err, total)
	}
	if err := s.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(ctx, "/Old-Page"); ok {
		t.Fatal("deleted redirect matched")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'redirects.%'`).Scan(&n)
	if n != 5 {
		t.Fatalf("audit entries: %d", n)
	}
}

func TestRedirectCacheReloadsAfterTTL(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, ok := s.Lookup(ctx, "/ext"); ok {
		t.Fatal("unexpected match")
	}
	// Another process (e.g. the WXR importer CLI) writes directly.
	if _, err := s.db.Exec(`INSERT INTO redirects (id, from_path, to_url, status, enabled, created_at) VALUES ('x', '/ext', '/new', 301, 1, ?)`, db.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(ctx, "/ext"); ok {
		t.Fatal("cache reloaded before the TTL")
	}
	now = now.Add(cacheTTL)
	if got, ok := s.Lookup(ctx, "/ext"); !ok || got.ToURL != "/new" {
		t.Fatalf("external write not seen after TTL: %v", ok)
	}
}

func TestRedirectLookupDoesNotReparseDecodedPaths(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, Redirect{FromPath: "/what%3F", ToURL: "/faq", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, Redirect{FromPath: "/100%25", ToURL: "/full", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/what?", "/what?/", "/100%"} { // r.URL.Path is already decoded
		if _, ok := s.Lookup(ctx, p); !ok {
			t.Errorf("decoded path %q not matched", p)
		}
	}
}

func TestRedirectRejectsSameHostAbsoluteLoop(t *testing.T) {
	s := testService(t)
	s.siteHost = func(context.Context) string { return "zad.example" }
	var ve *ValidationError
	if _, err := s.Save(context.Background(), Redirect{FromPath: "/a/", ToURL: "https://ZAD.example/a?x=1"}); !errors.As(err, &ve) || ve.Fields["to_url"] != "redirects.error.loop" {
		t.Fatalf("same-host loop: %v", err)
	}
	if _, err := s.Save(context.Background(), Redirect{FromPath: "/a", ToURL: "https://other.example/a", Enabled: true}); err != nil {
		t.Fatalf("other host: %v", err)
	}
	// The middleware also refuses to loop on the request host.
	h := Middleware(s)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	req := httptest.NewRequest("GET", "http://other.example/a", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("runtime loop guard: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRedirectReloadFailureKeepsOldTable(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.Save(ctx, Redirect{FromPath: "/old", ToURL: "/new", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(ctx, "/old"); !ok {
		t.Fatal("warm-up lookup")
	}
	good := s.db
	broken, _ := db.Open(filepath.Join(t.TempDir(), "broken.db")) // no redirects table
	t.Cleanup(func() { broken.Close() })
	s.db = broken
	now = now.Add(cacheTTL)
	if _, ok := s.Lookup(ctx, "/old"); !ok {
		t.Fatal("failed reload dropped the old table")
	}
	s.db = good
	if _, err := good.Exec(`DELETE FROM redirects`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(reloadBackoff / 2)
	if _, ok := s.Lookup(ctx, "/old"); !ok {
		t.Fatal("reloaded during backoff")
	}
	now = now.Add(reloadBackoff)
	if _, ok := s.Lookup(ctx, "/old"); ok {
		t.Fatal("not reloaded after backoff")
	}
}

func TestRedirectMiddleware301(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	a, _ := s.Save(ctx, Redirect{FromPath: "/old", ToURL: "/new", Enabled: true})
	s.Save(ctx, Redirect{FromPath: "/temp", ToURL: "https://example.com/t#frag", Status: 302, Enabled: true})
	s.Save(ctx, Redirect{FromPath: "/keepq", ToURL: "/target?x=1", Status: 307, Enabled: true})
	h := Middleware(s)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("page")) }))
	cases := []struct {
		method, path string
		code         int
		loc          string
	}{
		{"GET", "/old", 301, "/new"},
		{"GET", "/old/?utm=1", 301, "/new?utm=1"},
		{"HEAD", "/old", 301, "/new"},
		{"GET", "/temp?a=b", 302, "https://example.com/t?a=b#frag"},
		{"GET", "/keepq?y=2", 307, "/target?x=1"},
		{"POST", "/old", 200, ""},
		{"GET", "/OLD", 200, ""},
		{"GET", "/static/admin/old", 200, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.code || rec.Header().Get("Location") != c.loc {
			t.Errorf("%s %s: %d %q, want %d %q", c.method, c.path, rec.Code, rec.Header().Get("Location"), c.code, c.loc)
		}
	}
	if err := s.flushHits(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.get(ctx, a.ID); got.Hits != 3 {
		t.Fatalf("hits: %d", got.Hits)
	}
}

func TestImportCSV(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	s.Save(ctx, Redirect{FromPath: "/exists", ToURL: "/old-target", Enabled: false})
	in := "from,to,status\n" +
		"/a,/b\n" +
		"/exists,/new-target,302\n" +
		"/bad,javascript:x\n" +
		"/c,/d,999\n" +
		"/a/,/dup\n" +
		"onlyone\n" +
		"/e,https://example.com/e,308\n"
	res, err := s.ImportCSV(ctx, strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 || len(res.Errors) != 4 {
		t.Fatalf("result: %+v", res)
	}
	if res.Errors[0].Line != 4 || res.Errors[0].Key != "redirects.error.to_invalid" || res.Errors[2].Key != "redirects.import.error_duplicate" {
		t.Fatalf("errors: %+v", res.Errors)
	}
	if got, ok := s.Lookup(ctx, "/exists"); !ok || got.ToURL != "/new-target" || got.Status != 302 {
		t.Fatalf("upsert re-enables and updates: %+v %v", got, ok)
	}
	if got, ok := s.Lookup(ctx, "/e"); !ok || got.Status != 308 {
		t.Fatalf("imported: %+v", got)
	}
}

// M3: reserved application prefixes are refused at save time (service,
// so admin, API, CSV import and MCP all go through it) and skipped by the
// middleware even for rows stored earlier.
func TestRedirectReservedPrefixes(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	for _, p := range []string{"/admin", "/admin/", "/Admin/users", "/api/v1/x", "/mcp", "/auth/magic/x", "/webhooks/stripe",
		"/media/2026/a.png", "/static/admin/app.css", "/checkout/pro", "/account", "/account/open/1", "/healthz", "/readyz"} {
		_, err := s.Save(ctx, Redirect{FromPath: p, ToURL: "/elsewhere", Enabled: true})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields["from_path"] != "redirects.error.from_reserved" {
			t.Errorf("%s: want from_reserved, got %v", p, err)
		}
	}
	for _, p := range []string{"/administration", "/my-account", "/apis", "/accounting/x", "/media-kit"} {
		if _, err := s.Save(ctx, Redirect{FromPath: p, ToURL: "/elsewhere", Enabled: true}); err != nil {
			t.Errorf("%s must be allowed: %v", p, err)
		}
	}
	res, err := s.ImportCSV(ctx, strings.NewReader("from,to,status\n/checkout/x,/shop,301\n/old-shop,/shop,301\n"))
	if err != nil || res.Imported != 1 || len(res.Errors) != 1 || res.Errors[0].Key != "redirects.error.from_reserved" {
		t.Fatalf("CSV import: %+v %v", res, err)
	}
	// A row stored before the rule existed is never served.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO redirects (id, from_path, to_url, status, hits, enabled, created_at)
		VALUES ('legacy', '/account', 'https://evil.example/', 301, 0, 1, ?)`, db.Now()); err != nil {
		t.Fatal(err)
	}
	s.invalidate()
	h := Middleware(s)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("page")) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/account", nil))
	if rec.Code != 200 {
		t.Fatalf("reserved path redirected: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
