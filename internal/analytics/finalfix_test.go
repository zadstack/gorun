package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/site"
)

// arPage is an Arabic page path as stored (decoded) and as a browser's
// location.pathname reports it (percent-encoded, upper-case hex).
const arPage = "/ar/اتصل-بنا"

var arPageEncoded = (&url.URL{Path: arPage}).EscapedPath()

// TestMagicLinkNeverTracked: a live magic-link page (200 HTML, token not
// consumed) gets no script, and a beacon naming that path stores nothing.
func TestMagicLinkNeverTracked(t *testing.T) {
	e := bootCollect(t, nil, nil)
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	u, err := svc.Create(context.Background(), "editor@site.test", "Ed", "correct horse battery", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.NewMagicLink(context.Background(), e.app.Deps, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := e.get("/auth/magic/" + token)
	if resp.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(body), "</head>") {
		t.Fatalf("control: magic page %d, want a 200 HTML page", resp.StatusCode)
	}
	if strings.Contains(body, ScriptPath) {
		t.Error("script injected into the magic-link page")
	}
	e.beacon(`{"p":"/auth/magic/` + token + `"}`)
	e.beacon(`{"p":"/auth/magic/x"}`)
	e.beacon(`{"p":"/auth/anything"}`)
	if evs := e.events(); len(evs) != 0 {
		t.Fatalf("beacons under /auth/ stored: %+v", evs)
	}
	// The hard exclusion does not depend on the saved list.
	e.setting(SettingExclude, "")
	e.beacon(`{"p":"/auth/magic/x"}`)
	if evs := e.events(); len(evs) != 0 {
		t.Fatalf("beacon under /auth/ stored with an empty exclude list: %+v", evs)
	}
	// Submits from a page under /auth/ are dropped too.
	if err := e.app.Deps.Hooks.Do(context.Background(), hookFormSubmitted,
		site.FormSubmission{ID: "s1", Form: "contact", Page: "/auth/magic/x"}); err != nil {
		t.Fatal(err)
	}
	if forms, _, _ := storedSubmits(t, e); len(forms) != 0 {
		t.Fatalf("submit under /auth/ stored: %v", forms)
	}
}

func TestHardExcluded(t *testing.T) {
	for p, want := range map[string]bool{
		"/auth/magic/x": true, "/auth/": true, "/auth": true, "/admin": true, "/admin/x": true, "/_a/e": true,
		"/authors": false, "/administer": false, "/": false, "/blog/auth/x": false,
	} {
		if got := excluded(p, nil); got != want {
			t.Errorf("excluded(%q, nil) = %v, want %v", p, got, want)
		}
	}
}

// htmlPage writes a 200 HTML page with a head, marked as a theme render
// when themed.
func htmlPage(themed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if themed {
			httpx.MarkThemePage(r.Context())
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><head><title>x</title></head><body>x</body></html>"))
	}
}

func serveVia(m *Module, h http.Handler, target string) string {
	rec := httptest.NewRecorder()
	m.inject(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Body.String()
}

// TestInjectOnlyThemePages: a public 200 HTML response that the theme did
// not render gets no script; a theme render does.
func TestInjectOnlyThemePages(t *testing.T) {
	e := bootCollect(t, nil, nil)
	if body := serveVia(e.mod, htmlPage(false), "/some-module-page"); strings.Contains(body, ScriptPath) {
		t.Error("script on a non-theme HTML page")
	}
	if body := serveVia(e.mod, htmlPage(true), "/some-theme-page"); !strings.Contains(body, wantTag) {
		t.Error("no script on a theme-rendered page")
	}
	if body := serveVia(e.mod, htmlPage(true), "/auth/magic/x"); strings.Contains(body, ScriptPath) {
		t.Error("script under /auth/ even though the theme rendered it")
	}
	// The real theme still gets it (end to end).
	if _, home := e.get("/"); !strings.Contains(home, wantTag) {
		t.Error("no script on the theme home page")
	}
}

func TestCanonicalPath(t *testing.T) {
	for in, want := range map[string]string{
		"/a":             "/a",
		arPage:           arPageEncoded,
		arPageEncoded:    arPageEncoded,
		"/ar/%d8%a7":     "/ar/%D8%A7",
		"/a%20b":         "/a%20b",
		"/a b":           "/a%20b",
		"/bad%zz":        "/bad%zz", // not decodable: kept as given
		"/x%2Fy":         "/x/y",
		"/caf%C3%A9?q=1": "/caf%C3%A9%3Fq=1",
	} {
		if got := canonicalPath(in); got != want {
			t.Errorf("canonicalPath(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasPrefix(arPageEncoded, "/ar/%D8%A7") {
		t.Fatalf("fixture: %q", arPageEncoded)
	}
}

func TestCleanPathCanonical(t *testing.T) {
	for in, want := range map[string]string{
		arPage:                     arPageEncoded,
		arPageEncoded + "?utm_x=1": arPageEncoded,
		"/%2F%2Fevil.example":      "", // decodes to a protocol-relative path
	} {
		if got, _ := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
	// A cap never leaves a broken escape behind.
	long := "/" + strings.Repeat("ا", 200)
	got, _ := cleanPath(long)
	if len(got) > maxPath || strings.HasSuffix(got, "%") || (len(got) >= 2 && got[len(got)-2] == '%') {
		t.Fatalf("capped path %d bytes ends %q", len(got), got[max(0, len(got)-4):])
	}
	if d, err := url.PathUnescape(got); err != nil || !utf8.ValidString(d) {
		t.Fatalf("capped path does not decode to UTF-8: %q %v", d, err)
	}
}

// TestArabicFormsConversion: views (encoded, from location.pathname) and
// submits (decoded, from site's form source) of one Arabic page meet in
// the Forms conversion, and a Page filter keeps that page's submits.
func TestArabicFormsConversion(t *testing.T) {
	e := bootCollect(t, nil, nil)
	e.beacon(`{"p":"` + arPageEncoded + `"}`)
	e.beacon(`{"p":"`+arPageEncoded+`"}`, "User-Agent", strings.Replace(chromeUA, "130", "131", 1))
	if err := e.app.Deps.Hooks.Do(context.Background(), hookFormSubmitted,
		site.FormSubmission{ID: "s1", Form: "contact", Page: arPage}); err != nil {
		t.Fatal(err)
	}
	if evs := e.events(); len(evs) != 3 || evs[0].Path != arPageEncoded || evs[1].Path != arPageEncoded {
		t.Fatalf("events %+v", evs)
	}
	_, pages, _ := storedSubmits(t, e)
	if len(pages) != 1 || pages[0] != arPageEncoded {
		t.Fatalf("submit pages %q, want %q", pages, arPageEncoded)
	}
	now := time.Now().UTC()
	f := Filter{From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	for name, page := range map[string]string{"no filter": "", "page filter (stored key)": arPageEncoded, "page filter (decoded)": arPage} {
		ff := f
		ff.Page = canonicalPath(page)
		if page == "" {
			ff.Page = ""
		}
		rows, err := e.mod.store.Forms(context.Background(), ff)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Views != 2 || rows[0].Submits != 1 {
			t.Errorf("%s: forms %+v, want 2 views and 1 submit on %s", name, rows, arPageEncoded)
		}
	}
}

// TestDashboardPageFilterCanonical: a decoded ?page= value filters by the
// stored (encoded) key.
func TestDashboardPageFilterCanonical(t *testing.T) {
	e := bootCollect(t, nil, nil)
	a := &adminUI{m: e.mod}
	r := httptest.NewRequest(http.MethodGet, "/admin/analytics?page="+url.QueryEscape(arPage), nil)
	if got := a.viewOf(r).f.Page; got != arPageEncoded {
		t.Errorf("dashboard page filter = %q, want %q", got, arPageEncoded)
	}
	f, bad := apiRange(httptest.NewRequest(http.MethodGet, "/api?page="+url.QueryEscape(arPage), nil), time.Now())
	if len(bad) > 0 || f.Page != arPageEncoded {
		t.Errorf("API page filter = %q (%v), want %q", f.Page, bad, arPageEncoded)
	}
}

// TestArabicExclusion: an exclusion typed decoded and one pasted encoded
// both stop the script and the beacons of that page.
func TestArabicExclusion(t *testing.T) {
	for name, prefix := range map[string]string{"decoded": "/ar/اتصل", "encoded": (&url.URL{Path: "/ar/اتصل"}).EscapedPath()} {
		t.Run(name, func(t *testing.T) {
			e := bootCollect(t, nil, nil)
			saved, ok := cleanExclude(prefix + "\n/private")
			if !ok {
				t.Fatalf("cleanExclude rejected %q", prefix)
			}
			e.setting(SettingExclude, saved)
			if body := serveVia(e.mod, htmlPage(true), arPageEncoded); strings.Contains(body, ScriptPath) {
				t.Error("script on an excluded Arabic page")
			}
			if body := serveVia(e.mod, htmlPage(true), "/ar/other"); !strings.Contains(body, wantTag) {
				t.Error("exclusion leaked to /ar/other")
			}
			e.beacon(`{"p":"` + arPageEncoded + `"}`)
			e.beacon(`{"p":"/ar/other"}`)
			if evs := e.events(); len(evs) != 1 || evs[0].Path != "/ar/other" {
				t.Fatalf("events %+v, want only /ar/other", evs)
			}
			if err := e.app.Deps.Hooks.Do(context.Background(), hookFormSubmitted,
				site.FormSubmission{ID: "s1", Form: "contact", Page: arPage}); err != nil {
				t.Fatal(err)
			}
			if forms, _, _ := storedSubmits(t, e); len(forms) != 0 {
				t.Errorf("submit on an excluded Arabic page stored")
			}
			// Even a value saved raw (an older install) is canonicalised on read.
			e.setting(SettingExclude, prefix)
			e.beacon(`{"p":"` + arPageEncoded + `"}`)
			if evs := e.events(); len(evs) != 1 {
				t.Fatalf("raw saved exclusion %q not applied: %+v", prefix, evs)
			}
		})
	}
}

func TestRateKey(t *testing.T) {
	same := [][2]string{
		{"2001:db8:1:2::1", "2001:db8:1:2:ffff:ffff:ffff:ffff"},
		{"2001:db8:1:2::1", "2001:0db8:0001:0002:0000:0000:0000:0009"},
		{"203.0.113.7", "203.0.113.7"},
		{"", ""},
		{"garbage", ""},
	}
	for _, p := range same {
		if a, b := rateKey(p[0]), rateKey(p[1]); a != b {
			t.Errorf("rateKey(%q) = %q, rateKey(%q) = %q: want one bucket", p[0], a, p[1], b)
		}
	}
	diff := [][2]string{
		{"2001:db8:1:2::1", "2001:db8:1:3::1"},
		{"203.0.113.7", "203.0.113.8"},
		{"203.0.113.7", ""},
		{"2001:db8:1:2::1", ""},
	}
	for _, p := range diff {
		if a, b := rateKey(p[0]), rateKey(p[1]); a == b {
			t.Errorf("rateKey(%q) = rateKey(%q) = %q: want separate buckets", p[0], p[1], a)
		}
	}
	// One /64 shares the limit.
	l := newLimiter(2, time.Minute)
	now := time.Now()
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2::3"} {
		if got, want := l.allow(rateKey(ip), now), i < 2; got != want {
			t.Fatalf("hit %d from %s: allow = %v, want %v", i+1, ip, got, want)
		}
	}
	if !l.allow(rateKey("2001:db8:1:3::1"), now) {
		t.Fatal("another /64 was limited")
	}
}
