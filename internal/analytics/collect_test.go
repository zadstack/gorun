package analytics

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

const (
	chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	wantTag  = `<script defer src="/_a/s.js"></script>`
)

type fakeGeo map[string]string

func (g fakeGeo) Country(ip string) string {
	if c, ok := g[ip]; ok {
		return c
	}
	return unknownCountry
}

type collectEnv struct {
	t   *testing.T
	app *kernel.App
	mod *Module
	srv *httptest.Server
	c   *http.Client
}

// bootCollect starts settings + auth + fake content + theme (repo themes/)
// + site + analytics behind an httptest server.
func bootCollect(t *testing.T, tweak func(*config.Config), g geo) *collectEnv {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://site.test", Theme: "default"}
	if tweak != nil {
		tweak(&cfg)
	}
	mod := New()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir("../../themes")), site.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		mod.geo = g // before the server starts serving
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &collectEnv{t: t, app: app, mod: mod, srv: srv, c: newJarClient()}
}

func newJarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *collectEnv) do(method, path string, body io.Reader, hdr ...string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (e *collectEnv) get(path string) (*http.Response, string) {
	e.t.Helper()
	return e.do("GET", path, nil)
}

// beacon posts body to /_a/e as a browser's sendBeacon would (text/plain),
// with a desktop Chrome user agent unless hdr overrides it.
func (e *collectEnv) beacon(body string, hdr ...string) *http.Response {
	e.t.Helper()
	h := append([]string{"User-Agent", chromeUA, "Content-Type", "text/plain;charset=UTF-8"}, hdr...)
	resp, _ := e.do("POST", "/_a/e", strings.NewReader(body), h...)
	return resp
}

func (e *collectEnv) setting(key string, v any) {
	e.t.Helper()
	if err := e.app.Deps.Settings.Set(context.Background(), key, v); err != nil {
		e.t.Fatal(err)
	}
}

// events flushes the buffer and returns every stored event.
func (e *collectEnv) events() []Event {
	e.t.Helper()
	if err := e.mod.buf.Flush(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	var out []Event
	err := e.mod.store.With(context.Background(), func(d *sql.DB) error {
		rows, err := d.Query(`SELECT kind, path, visitor, ref_host, utm_source, utm_medium, utm_campaign, browser, os, device, country, locale FROM events ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev Event
			if err := rows.Scan(&ev.Kind, &ev.Path, &ev.Visitor, &ev.RefHost, &ev.UTMSource, &ev.UTMMedium, &ev.UTMCampaign,
				&ev.Browser, &ev.OS, &ev.Device, &ev.Country, &ev.Locale); err != nil {
				return err
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *collectEnv) createUser(email, role string) {
	e.t.Helper()
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), email, "User", "correct horse battery", role); err != nil {
		e.t.Fatal(err)
	}
}

var csrfFieldRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// login creates a user with role and signs the env client in.
func (e *collectEnv) login(email, role string) {
	e.t.Helper()
	e.createUser(email, role)
	_, page := e.get("/admin/login")
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf on login page:\n%s", page)
	}
	form := url.Values{"_csrf": {m[1]}, "email": {email}, "password": {"correct horse battery"}}
	resp, _ := e.do("POST", "/admin/login", strings.NewReader(form.Encode()), "Content-Type", "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: %d", resp.StatusCode)
	}
}

const goodBeacon = `{"p":"/hello","r":"https://www.google.com/search?q=x","w":1280,"l":"ar"}`

func TestScriptServed(t *testing.T) {
	e := bootCollect(t, nil, nil)
	resp, body := e.get(ScriptPath)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := len(body); n == 0 || n >= 1024 {
		t.Fatalf("script is %d bytes, want 1..1023", n)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if body != script {
		t.Error("served body differs from the embedded script")
	}
}

// TestModuleEnabledServesScript is the enabled counterpart of
// TestModuleDisabled: the same minimal boot serves /_a/s.js.
func TestModuleEnabledServesScript(t *testing.T) {
	app, _ := bootApp(t, nil)
	srv := httptest.NewServer(app.Handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL + ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/_a/s.js = %d, want 200", resp.StatusCode)
	}
}

// TestScriptIgnoresHashChange checks the script source; the browser
// behaviour is covered by the Task 6 e2e. A hash-only change must not send:
// the script reacts only to pushState and popstate, and compares
// location.pathname (plus the utm query) with the last value it sent.
func TestScriptIgnoresHashChange(t *testing.T) {
	for _, want := range []string{"pushState", `"popstate"`, "location", ".pathname", "sendBeacon", "keepalive", `doNotTrack=="1"`, "globalPrivacyControl===true", `"/_a/e"`} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	for _, bad := range []string{"hashchange", "replaceState", ".hash", ".href", "document.cookie", "localStorage", "sessionStorage"} {
		if strings.Contains(script, bad) {
			t.Errorf("script must not use %q", bad)
		}
	}
	if !regexp.MustCompile(`p===last|last===p|p==last`).MatchString(script) {
		t.Error("script does not compare the path with the last one sent")
	}
}

func TestScriptInjectedOnPublicHTML(t *testing.T) {
	e := bootCollect(t, nil, nil)
	resp, body := e.get("/")
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if n := strings.Count(body, wantTag); n != 1 {
		t.Fatalf("script tag %d times, want 1:\n%s", n, body)
	}
	i, j := strings.Index(body, wantTag), strings.Index(strings.ToLower(body), "</head>")
	if j < 0 || i+len(wantTag) != j {
		t.Fatalf("tag at %d, </head> at %d: want the tag right before </head>", i, j)
	}
	// The same-origin script is allowed by the site CSP as it stands.
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP does not allow same-origin scripts: %q", csp)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %s, body %d", cl, len(body))
	}
	// Script and beacon are not themselves rewritten.
	if _, js := e.get(ScriptPath); strings.Contains(js, "<script") {
		t.Error("script rewritten")
	}
}

func TestNoInjectionForAdminPreviewStaffOrExcluded(t *testing.T) {
	t.Run("admin", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		e.createUser("admin@site.test", auth.RoleAdmin) // else /admin/login redirects to setup
		resp, body := e.get("/admin/login")
		if resp.StatusCode != 200 {
			t.Fatalf("login page %d", resp.StatusCode)
		}
		if strings.Contains(body, ScriptPath) {
			t.Error("script on an admin page")
		}
	})
	t.Run("preview", func(t *testing.T) {
		if previewParam != theme.PreviewParam {
			t.Fatalf("previewParam %q drifted from theme.PreviewParam %q", previewParam, theme.PreviewParam)
		}
		e := bootCollect(t, nil, nil)
		if _, home := e.get("/"); !strings.Contains(home, wantTag) {
			t.Fatal("control: no tag on /")
		}
		_, body := e.get("/?" + theme.PreviewParam + "=1")
		if strings.Contains(body, ScriptPath) {
			t.Error("script on a theme preview")
		}
	})
	t.Run("staff", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		if _, home := e.get("/"); !strings.Contains(home, wantTag) {
			t.Fatal("control: no tag for a visitor")
		}
		e.login("admin@site.test", auth.RoleAdmin)
		resp, body := e.get("/")
		if resp.StatusCode != 200 {
			t.Fatalf("GET / = %d", resp.StatusCode)
		}
		if strings.Contains(body, ScriptPath) {
			t.Error("script for a signed-in user with content.edit")
		}
	})
	t.Run("excluded", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		e.setting(SettingExclude, "/private\n  /search  \n")
		resp, body := e.get("/search")
		if resp.StatusCode != 200 {
			t.Fatalf("GET /search = %d", resp.StatusCode)
		}
		if strings.Contains(body, ScriptPath) {
			t.Error("script on an excluded path")
		}
		if _, home := e.get("/"); !strings.Contains(home, wantTag) {
			t.Error("exclusion leaked to /")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		if _, home := e.get("/"); !strings.Contains(home, wantTag) {
			t.Fatal("control: no tag while on")
		}
		e.setting(SettingEnabled, false)
		_, body := e.get("/")
		if strings.Contains(body, ScriptPath) {
			t.Error("script while tracking is off")
		}
		e.beacon(goodBeacon)
		if n := len(e.events()); n != 0 {
			t.Errorf("%d events while tracking is off", n)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		resp, body := e.get("/no-such-page")
		if resp.StatusCode != 404 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if strings.Contains(body, ScriptPath) {
			t.Error("script on a 404")
		}
	})
}

func TestBeaconRecordsView(t *testing.T) {
	e := bootCollect(t, nil, fakeGeo{"127.0.0.1": "AE"})
	resp := e.beacon(goodBeacon, "Origin", "http://site.test", "Sec-Fetch-Site", "same-origin")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	evs := e.events()
	if len(evs) != 1 {
		t.Fatalf("%d events, want 1", len(evs))
	}
	ev := evs[0]
	want := Event{Kind: KindView, Path: "/hello", RefHost: "www.google.com", Browser: "Chrome", OS: "macOS", Device: "desktop", Country: "AE", Locale: "ar"}
	got := ev
	got.Visitor = ""
	if got != want {
		t.Fatalf("event = %+v\nwant    %+v", got, want)
	}
	if len(ev.Visitor) != 16 {
		t.Errorf("visitor %q, want 16 hex chars", ev.Visitor)
	}
}

func TestBeaconNilGeoIsUnknownCountry(t *testing.T) {
	e := bootCollect(t, nil, nil)
	e.beacon(goodBeacon)
	evs := e.events()
	if len(evs) != 1 || evs[0].Country != "--" {
		t.Fatalf("events %+v", evs)
	}
}

func TestBeaconAlways204(t *testing.T) {
	cases := []struct {
		name string
		body string
		hdr  []string
	}{
		{"bad json", `{"p":`, nil},
		{"bot", goodBeacon, []string{"User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}},
		{"cross origin", goodBeacon, []string{"Origin", "http://evil.test"}},
		{"cross site", goodBeacon, []string{"Sec-Fetch-Site", "cross-site"}},
		{"same site other origin", goodBeacon, []string{"Sec-Fetch-Site", "same-site"}},
		{"too large", `{"p":"/` + strings.Repeat("a", 2100) + `"}`, nil},
		{"no path", `{"r":"","w":100}`, nil},
		{"absolute path", `{"p":"https://evil.test/x"}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := bootCollect(t, nil, nil)
			if resp := e.beacon(c.body, c.hdr...); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if n := len(e.events()); n != 0 {
				t.Fatalf("%d events recorded", n)
			}
		})
	}
	t.Run("rate limit", func(t *testing.T) {
		e := bootCollect(t, nil, nil)
		for i := range rateLimit + 1 {
			if resp := e.beacon(goodBeacon); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("beacon %d: status %d", i+1, resp.StatusCode)
			}
		}
		if n := len(e.events()); n != rateLimit {
			t.Fatalf("%d events, want %d (the 121st dropped)", n, rateLimit)
		}
	})
}

func TestBeaconStripsQueryAndCapsPath(t *testing.T) {
	e := bootCollect(t, nil, nil)
	e.beacon(`{"p":"/a?token=x&utm_source=nl","w":500}`)
	e.beacon(`{"p":"/` + strings.Repeat("b", 599) + `","w":500}`)
	evs := e.events()
	if len(evs) != 2 {
		t.Fatalf("%d events", len(evs))
	}
	if evs[0].Path != "/a" || evs[0].UTMSource != "nl" || evs[0].UTMMedium != "" {
		t.Errorf("first = %+v", evs[0])
	}
	if len(evs[1].Path) != 512 {
		t.Errorf("long path stored with %d chars, want 512", len(evs[1].Path))
	}
	if evs[0].RefHost != "direct" || evs[0].Device != "mobile" {
		t.Errorf("ref/device = %q %q", evs[0].RefHost, evs[0].Device)
	}
}

func TestVisitorUsesTrustedClientIP(t *testing.T) {
	e := bootCollect(t, func(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1/32", "::1/128"} }, nil)
	e.beacon(goodBeacon, "X-Forwarded-For", "203.0.113.7")
	e.beacon(goodBeacon, "X-Forwarded-For", "198.51.100.9")
	e.beacon(goodBeacon, "X-Forwarded-For", "203.0.113.7")
	evs := e.events()
	if len(evs) != 3 {
		t.Fatalf("%d events", len(evs))
	}
	if evs[0].Visitor == evs[1].Visitor {
		t.Error("two client IPs behind the proxy share one visitor")
	}
	if evs[0].Visitor != evs[2].Visitor {
		t.Error("one client IP gave two visitors")
	}

	// Without a trusted proxy the forwarded header is ignored.
	u := bootCollect(t, nil, nil)
	u.beacon(goodBeacon, "X-Forwarded-For", "203.0.113.7")
	u.beacon(goodBeacon, "X-Forwarded-For", "198.51.100.9")
	if evs := u.events(); len(evs) != 2 || evs[0].Visitor != evs[1].Visitor {
		t.Errorf("untrusted X-Forwarded-For changed the visitor: %+v", evs)
	}
}

func TestStaffBeaconIgnored(t *testing.T) {
	e := bootCollect(t, nil, nil)
	e.login("admin@site.test", auth.RoleAdmin)
	if resp := e.beacon(goodBeacon); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := len(e.events()); n != 0 {
		t.Fatalf("staff beacon recorded (%d events)", n)
	}
}

func TestBeaconExcludedPathIgnored(t *testing.T) {
	e := bootCollect(t, nil, nil)
	e.setting(SettingExclude, "/private")
	e.beacon(`{"p":"/private/x"}`)
	e.beacon(`{"p":"/public"}`)
	if evs := e.events(); len(evs) != 1 || evs[0].Path != "/public" {
		t.Fatalf("events %+v", evs)
	}
}

func TestSiteIDStable(t *testing.T) {
	a, b := siteIDFor([]byte("k1")), siteIDFor([]byte("k1"))
	if a != b || len(a) != 16 || a == siteIDFor([]byte("k2")) {
		t.Fatalf("siteID %q %q", a, b)
	}
	if strings.Contains(a, "k1") {
		t.Fatal("raw key in site ID")
	}
}

func TestOpenGeoMissingOrBadIsNil(t *testing.T) {
	dir := t.TempDir()
	g, closeFn := openGeo(dir, nil)
	defer closeFn()
	if g.Country("8.8.8.8") != "--" {
		t.Error("missing file: country not --")
	}
	if err := os.WriteFile(filepath.Join(dir, GeoFile), []byte("not an mmdb"), 0o644); err != nil {
		t.Fatal(err)
	}
	g2, close2 := openGeo(dir, nil)
	defer close2()
	if g2.Country("8.8.8.8") != "--" {
		t.Error("bad file: country not --")
	}
	if (mmdbGeo{}).Country("not-an-ip") != "--" {
		t.Error("bad ip")
	}
}

// serveInjected runs h behind an injectWriter that always wants the tag.
func serveInjected(h http.HandlerFunc) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	iw := &injectWriter{ResponseWriter: rec, want: func() bool { return true }}
	h(iw, httptest.NewRequest("GET", "/", nil))
	iw.finish()
	return rec
}

func TestInjectWriter(t *testing.T) {
	page := "<!doctype html><HTML><HEAD><title>x</title></HEAD><body>hi</body></html>"
	t.Run("html with content-length", func(t *testing.T) {
		rec := serveInjected(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(page)))
			w.Header().Set("ETag", `"abc"`)
			_, _ = io.WriteString(w, page[:20])
			_, _ = io.WriteString(w, page[20:])
		})
		want := strings.Replace(page, "</HEAD>", wantTag+"</HEAD>", 1)
		if rec.Body.String() != want {
			t.Fatalf("body %q", rec.Body.String())
		}
		if rec.Header().Get("Content-Length") != strconv.Itoa(len(want)) || rec.Header().Get("ETag") != "" {
			t.Errorf("headers %v", rec.Header())
		}
	})
	untouched := map[string]http.HandlerFunc{
		"json": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, page)
		},
		"no content type": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, page) },
		"404": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			_, _ = io.WriteString(w, page)
		},
		"encoded": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = io.WriteString(w, page)
		},
		"streaming": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, page[:20])
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, page[20:])
		},
		"no head": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<p>fragment</p>")
		},
	}
	for name, h := range untouched {
		t.Run(name, func(t *testing.T) {
			rec := serveInjected(h)
			if strings.Contains(rec.Body.String(), ScriptPath) {
				t.Fatalf("injected: %q", rec.Body.String())
			}
			if name != "no head" && rec.Body.String() != page {
				t.Fatalf("body changed: %q", rec.Body.String())
			}
		})
	}
	t.Run("not wanted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		iw := &injectWriter{ResponseWriter: rec, want: func() bool { return false }}
		iw.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(iw, page)
		iw.finish()
		if rec.Body.String() != page {
			t.Fatalf("body %q", rec.Body.String())
		}
	})
}

func TestLimiterWindow(t *testing.T) {
	l := newLimiter(2, time.Minute)
	t0 := time.Now()
	for i, want := range []bool{true, true, false} {
		if got := l.allow("a", t0); got != want {
			t.Fatalf("hit %d: allow = %v, want %v", i+1, got, want)
		}
	}
	if !l.allow("b", t0) {
		t.Fatal("keys not independent")
	}
	if !l.allow("a", t0.Add(time.Minute)) {
		t.Fatal("window did not roll over")
	}
}
