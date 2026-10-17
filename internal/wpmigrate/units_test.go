package wpmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/wpmigrate/gutenberg"
)

func TestClientRetriesAndErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch r.URL.Path {
		case "/wp-json/flaky":
			if n < 3 {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write([]byte(`[{"id":1},{"id":2}]`))
		case "/wp-json/forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"rest_forbidden","message":"no"}`))
		default:
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL+"/", "", "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.backoff = time.Millisecond
	var ids []string
	total, err := c.All(context.Background(), "/flaky", nil, 0, func(r json.RawMessage) error { ids = append(ids, string(r)); return nil })
	if err != nil || total != 2 || len(ids) != 2 || calls.Load() != 3 {
		t.Fatalf("retry: total=%d ids=%v calls=%d err=%v", total, ids, calls.Load(), err)
	}
	_, err = c.Get(context.Background(), "/forbidden", nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != "rest_forbidden" || !IsAuthError(err) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("status error = %v", err)
	}
	var out map[string]any
	if _, err := c.GetJSON(context.Background(), "/other", nil, &out); !errors.Is(err, ErrNotWordPress) {
		t.Fatalf("invalid json = %v", err)
	}
	for _, bad := range []string{"ftp://x", "https://user:pw@x.example", "nope", ""} {
		if _, err := NewClient(bad, "", "", srv.Client()); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("%q accepted", bad)
		}
	}
	dead, _ := NewClient("http://127.0.0.1:1", "", "", &http.Client{Timeout: time.Second})
	dead.backoff = time.Millisecond
	if _, err := dead.Get(context.Background(), "/", nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("unreachable = %v", err)
	}
}

func TestHelpers(t *testing.T) {
	cases := map[string]int64{"99": 9900, "11990.50": 1199050, "0.015": 1, "1,299.9": 129990, "": 0}
	for in, want := range cases {
		if got, err := decimalToMinor(in, 2); err != nil || got != want {
			t.Errorf("decimalToMinor(%q) = %d %v, want %d", in, got, err, want)
		}
	}
	if _, err := decimalToMinor("abc", 2); err == nil {
		t.Error("invalid price accepted")
	}
	if wpTime("2026-08-31T10:00:00") != "2026-08-31T10:00:00Z" || wpTime("2026-08-31 10:00:00") != "2026-08-31T10:00:00Z" || wpTime("0000-00-00 00:00:00") != "" {
		t.Error("wpTime")
	}
	for in, want := range map[string]string{"publish": "published", "future": "scheduled", "private": "private", "pending": "pending", "draft": "draft", "auto-draft": "draft"} {
		if string(mapStatus(in)) != want {
			t.Errorf("mapStatus(%s)", in)
		}
	}
	for in, want := range map[string]string{"menu_1": "primary", "footer-menu": "footer", "primary": "primary", "": "", "sidebar": ""} {
		if got := menuLocation(in); got != want {
			t.Errorf("menuLocation(%q) = %q", in, got)
		}
	}
	if menuSlug(WPMenu{ID: 7, Slug: "القائمة"}) != "menu-7" || menuSlug(WPMenu{ID: 7, Slug: "Main"}) != "main" {
		t.Error("menuSlug")
	}
	hosts := map[string]bool{"example.org": true}
	if relativeURL("https://www.example.org/pricing/?a=1#x", hosts) != "/pricing/?a=1#x" || relativeURL("https://github.com/x", hosts) != "https://github.com/x" {
		t.Error("relativeURL")
	}
	if got := gutenberg.Autop("سطر أول\nسطر ثان\n\nفقرة"); got != "<p>سطر أول<br>\nسطر ثان</p>\n<p>فقرة</p>\n" {
		t.Errorf("autop = %q", got)
	}
	if fileNameOf("https://x/wp-content/uploads/%D8%BA%D9%84%D8%A7%D9%81.png") != "غلاف.png" || fileNameOf("https://x/") != "file" {
		t.Error("fileNameOf")
	}
	if variantFor(150) != "thumbnail" || variantFor(700) != "medium" || variantFor(1200) != "large" || variantFor(2560) != "" {
		t.Error("variantFor")
	}
	if decodeSlug("%d8%a7%d9%84") != "ال" || decodeSlug("%zz") != "%zz" {
		t.Error("decodeSlug")
	}
}

func TestHealthCheckStatuses(t *testing.T) {
	e := newEnv(t)
	check := func() admin.HealthCheck { return e.svc.healthCheck(e.ctx()) }
	if hc := check(); hc.Status != admin.CheckOK {
		t.Fatalf("no migrations = %+v", hc)
	}
	id := e.wxrMigration(Options{})
	e.app.Deps.DB.Exec(`UPDATE migrations SET status = 'failed' WHERE id = ?`, id)
	if hc := check(); hc.Status != admin.CheckFail {
		t.Fatalf("failed = %+v", hc)
	}
	e.app.Deps.DB.Exec(`UPDATE migrations SET status = 'paused' WHERE id = ?`, id)
	if hc := check(); hc.Status != admin.CheckWarn {
		t.Fatalf("paused = %+v", hc)
	}
	// Control errors.
	if err := e.svc.Resume(e.ctx(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resume missing = %v", err)
	}
	e.app.Deps.DB.Exec(`UPDATE migrations SET status = 'running' WHERE id = ?`, id)
	if err := e.svc.Start(e.ctx(), id); !errors.Is(err, ErrBadState) {
		t.Fatalf("start running = %v", err)
	}
	if err := e.svc.Resume(e.ctx(), id); !errors.Is(err, ErrBadState) {
		t.Fatalf("resume running = %v", err)
	}
	if err := e.svc.SaveMapping(e.ctx(), id, Mapping{}); !errors.Is(err, ErrBadState) {
		t.Fatalf("map running = %v", err)
	}
}

func TestShortcodesCurrencyAndDates(t *testing.T) {
	got := gutenberg.Shortcodes(`[PDF] [1] [Note] [ep_plans group="a"] [/ep_plans] [contact-form-7 id="2"]`)
	if len(got) != 2 || got["ep_plans"] != 1 || got["contact-form-7"] != 1 {
		t.Fatalf("shortcodes = %v", got)
	}
	if minorUnits("JPY") != 0 || minorUnits("kwd") != 3 || minorUnits("AED") != 2 || minorUnits("") != 2 {
		t.Fatal("minorUnits")
	}
	if n, _ := decimalToMinor("12.345", minorUnits("KWD")); n != 12345 {
		t.Fatalf("KWD minor = %d", n)
	}
}

func TestRejectedCredentialsNeverCompleteWithPublicFallback(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(true, Options{})
	if err := e.svc.Start(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	e.wp.fail("/wp-json/wp/v2/users/me", http.StatusForbidden)
	e.drain(id)
	st, err := e.svc.Status(e.ctx(), id)
	if err != nil || st.Status != "failed" {
		t.Fatalf("rejected auth must fail: %+v %v", st, err)
	}
	if st.Counts["content"]["done"] != 0 {
		t.Fatal("content imported after rejected credentials")
	}
}

func TestMarkdownEscaping(t *testing.T) {
	r := &ValidateReport{MigrationID: "m", BrokenLinks: []LinkIssue{{Title: "a|b `x` <script>", URL: "/p|q`"}}}
	out := r.markdown()
	if strings.Contains(out, "<script>") || strings.Contains(out, "a|b") || strings.Contains(out, "`x`") || !strings.Contains(out, `a\|b 'x' &lt;script>`) {
		t.Fatalf("markdown not escaped:\n%s", out)
	}
}

// TestWatchdogFailsStuckItems: a pending item of the current phase with no
// job for over 10 minutes is failed as "stuck" and the run advances.
func TestWatchdogFailsStuckItems(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if err := e.svc.Start(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	// Run the users step, then lose its item jobs.
	if _, err := e.q.drain(e.ctx(), 1); err != nil {
		t.Fatal(err)
	}
	e.q.mu.Lock()
	e.q.pending = nil
	e.q.mu.Unlock()
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	e.app.Deps.DB.Exec(`UPDATE migration_items SET updated_at = ? WHERE migration_id = ?`, old, id)
	fresh := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND status = 'pending'`, id)
	if fresh == 0 {
		t.Fatal("expected pending items")
	}
	if err := e.svc.watchdog(e.ctx()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM migration_items WHERE migration_id = ? AND error = 'stuck'`, id); n != fresh {
		t.Fatalf("stuck items = %d, want %d", n, fresh)
	}
	st := e.drain(id)
	if st.Status != StatusValidated || st.Counts["content"][ItemDone] != 3 {
		t.Fatalf("run did not continue: %s %v", st.Status, st.Counts)
	}
	// Retry recovers the stuck items.
	if err := e.svc.RetryFailed(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	if st = e.drain(id); st.Failed != 0 {
		t.Fatalf("after retry: %+v", st.Counts)
	}
}

// TestSiteSettingsNeverClobberAdminEdits: the title is written on a fresh
// site, refreshed while it still holds the migrated value, and kept once
// an admin changed it.
func TestSiteSettingsNeverClobberAdminEdits(t *testing.T) {
	e := newEnv(t)
	st := e.app.Deps.Settings
	id := e.wxrMigration(Options{})
	e.run(id)
	if got := st.GetString(e.ctx(), "site.title", ""); got != "EXAMPLE PRESS" {
		t.Fatalf("fresh site title = %q", got)
	}
	// WordPress renamed the site: the migrated value is refreshed.
	m, _ := e.svc.getMigration(e.ctx(), id)
	src, _ := e.svc.getSource(e.ctx(), m.SourceID)
	b, _ := os.ReadFile(src.filePath)
	_ = os.WriteFile(src.filePath, bytes.Replace(b, []byte("<title>EXAMPLE PRESS</title>"), []byte("<title>EXAMPLE PRESS 2</title>"), 1), 0o600)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(src.filePath, future, future)
	e.run(id)
	if got := st.GetString(e.ctx(), "site.title", ""); got != "EXAMPLE PRESS 2" {
		t.Fatalf("refreshed title = %q", got)
	}
	// An admin edit is never overwritten.
	if err := st.Set(e.ctx(), "site.title", "Admin title"); err != nil {
		t.Fatal(err)
	}
	e.run(id)
	if got := st.GetString(e.ctx(), "site.title", ""); got != "Admin title" {
		t.Fatalf("admin edit clobbered: %q", got)
	}
}
