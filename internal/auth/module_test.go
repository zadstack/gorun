package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

type site struct {
	t   *testing.T
	srv *httptest.Server
	app *kernel.App
	c   *http.Client
}

// adminStub stands in for the admin module (another package, booted after
// auth), which owns GET /admin. It renders pages/dashboard.html through the
// auth-published admin renderer so the shell (layout, topbar, sidebar) is
// exercised end to end.
type adminStub struct{}

func (adminStub) Name() string      { return "admin" }
func (adminStub) Migrations() fs.FS { return nil }
func (adminStub) Register(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("admin templates not published by auth")
	}
	d.Admin.AddNav(kernel.NavItem{ID: "stub.agents", LabelKey: "core.nav.dashboard", Icon: "bot", Href: "/admin/agents", Group: "agents"})
	d.Admin.AddNav(kernel.NavItem{ID: "stub.kit", LabelKey: "core.nav.dashboard", Icon: "box", Href: "/admin/kit", Group: "field_kit"})
	d.Router.Admin.Get("/", func(w http.ResponseWriter, r *http.Request) {
		tpl.Render(w, r, "pages/dashboard.html", struct{ Widgets []template.HTML }{})
	})
	return nil
}

func boot(t *testing.T) *site {
	t.Helper()
	return bootWith(t, adminStub{})
}

func bootWith(t *testing.T, extra ...kernel.Module) *site {
	t.Helper()
	return bootCfg(t, nil, extra...)
}

// bootCfg is bootWith with the config adjusted by mutate.
func bootCfg(t *testing.T, mutate func(*config.Config), extra ...kernel.Module) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	if mutate != nil {
		mutate(&cfg)
	}
	mods := append([]kernel.Module{settingsmod.New(), auth.New()}, extra...)
	app, err := kernel.Boot(context.Background(), cfg, mods)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &site{t: t, srv: srv, app: app, c: c}
}

func (s *site) do(method, path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no csrf field in:\n%s", html)
	}
	return m[1]
}

func TestSetupCreatesFirstAdminAndLogsIn(t *testing.T) {
	s := boot(t)
	resp, _ := s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login?next=%2Fadmin" {
		t.Fatalf("anonymous /admin: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = s.do("GET", "/admin/login", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/setup" {
		t.Fatalf("login with no users should go to setup: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page := s.do("GET", "/admin/setup", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `dir="rtl"`) {
		t.Fatalf("setup page: %d", resp.StatusCode)
	}
	token := csrfFrom(t, page)

	resp, page = s.do("POST", "/admin/setup", url.Values{"_csrf": {token}, "name": {"Ahmed"}, "email": {"bad"}, "password": {"short"}, "title": {"Studio"}, "locale": {"ar"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("invalid setup should re-render with errors: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/admin/setup", url.Values{"name": {"Ahmed"}, "email": {"admin@example.com"}, "password": {"correct horse battery"}, "title": {"Studio"}, "locale": {"ar"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("setup without csrf must be rejected: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/admin/setup", url.Values{"_csrf": {token}, "name": {"Ahmed"}, "email": {"admin@example.com"}, "password": {"correct horse battery"}, "title": {"Studio"}, "tagline": {"مرحبا"}, "locale": {"ar"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/workspace" {
		t.Fatalf("setup submit: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "site.title", ""); got != "Studio" {
		t.Fatalf("setup left site.title=%q", got)
	}
	resp, page = s.do("GET", "/admin", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "Ahmed") || !strings.Contains(page, "/static/admin/admin.css") {
		t.Fatalf("dashboard after setup: %d\n%s", resp.StatusCode, page)
	}
	// topbar live search with the full-page form fallback
	for _, want := range []string{`action="/admin/search"`, `hx-get="/admin/x/search"`, `hx-target="#admin-search-results"`,
		`hx-trigger="input changed delay:300ms, focus"`, `id="admin-search-results"`, `@click.outside=`, `@keydown.slash.window=`, `@keydown.escape=`} {
		if !strings.Contains(page, want) {
			t.Errorf("topbar lacks %s", want)
		}
	}
	// Compact sections use localized labels; custom groups remain in the
	// recovery/customization page without adding another primary section.
	if !strings.Contains(page, "الكانفس والوكلاء") || !strings.Contains(page, `data-navigation-section="settings"`) || strings.Contains(page, "core.nav.section.") {
		t.Fatalf("sidebar section labels:\n%s", page)
	}
	_, preferences := s.do("GET", "/admin/navigation", nil)
	if !strings.Contains(preferences, "Field Kit") || strings.Contains(preferences, "core.nav.group.") {
		t.Fatalf("custom group missing from preferences:\n%s", preferences)
	}
	if resp, _ := s.do("GET", "/admin/setup", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup must close once a user exists: %d", resp.StatusCode)
	}
	entries, _, _ := s.app.Deps.Audit.List(context.Background(), audit.ListQuery{Action: "user.setup"})
	if len(entries) != 1 || entries[0].Actor.Kind != "user" {
		t.Fatalf("audit entry: %+v", entries)
	}
	resp, css := s.do("GET", "/static/admin/admin.css", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("admin.css: %d", resp.StatusCode)
	}
	// Arabic micro-labels: no uppercase, letter-spacing or mono in RTL.
	// dir="ltr" islands keep the Latin style.
	for _, rule := range []string{`[dir="rtl"] .uppercase:not([dir="ltr"], [dir="ltr"] *) { text-transform: none; }`,
		`[dir="rtl"] [class*="tracking-"]:not([dir="ltr"], [dir="ltr"] *) { letter-spacing: normal; }`,
		`[dir="rtl"] .font-mono.uppercase:not([dir="ltr"], [dir="ltr"] *) { font-family: inherit; }`} {
		if !strings.Contains(css, rule) {
			t.Errorf("admin.css lacks %s", rule)
		}
	}
}

func TestLaunchCSSEmbeddedAtStaticPath(t *testing.T) {
	s := bootWith(t)
	resp, body := s.do("GET", httpx.LaunchCSSPath, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("launch.css: %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content-type: %q", ct)
	}
	for _, want := range []string{"--journey-bg", ".launch-page", ".launch-start", ".start-form"} {
		if !strings.Contains(body, want) {
			t.Errorf("launch.css missing %q", want)
		}
	}
	resp, _ = s.do("GET", httpx.LaunchCSSPath+"?v=test", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != httpx.CacheImmutable {
		t.Fatalf("versioned launch.css cache: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp, js := s.do("GET", httpx.LaunchPolishPath, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") || !strings.Contains(js, "launch-brand") {
		t.Fatalf("launch-polish.js: %d %q", resp.StatusCode, js)
	}
}

func TestAuthAloneServesNoDashboard(t *testing.T) {
	s := bootWith(t) // auth without the admin module
	resp, _ := s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login?next=%2Fadmin" {
		t.Fatalf("anonymous /admin must still redirect to login: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	svc, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "a@example.com", "A", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	resp, _ = s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"a@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("login: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := s.do("GET", "/admin", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/admin is the admin module's route; auth alone must 404, got %d", resp.StatusCode)
	}
}

func TestLoginLogoutAndAnonymousRedirect(t *testing.T) {
	s := boot(t)
	svc, ok := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if !ok {
		t.Fatal("auth service not registered")
	}
	if _, err := svc.Create(context.Background(), "ed@example.com", "Editor", "correct horse battery", "editor"); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.do("GET", "/admin/anything", nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
		t.Fatalf("anonymous: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page := s.do("GET", "/admin/login", nil)
	token := csrfFrom(t, page)
	resp, page = s.do("POST", "/admin/login", url.Values{"_csrf": {token}, "email": {"ed@example.com"}, "password": {"wrong password"}})
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(page, "role=\"alert\"") {
		t.Fatalf("bad password: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/admin/login", url.Values{"_csrf": {token}, "email": {"ed@example.com"}, "password": {"correct horse battery"}, "next": {"//evil.example"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("login: %d %q (open redirect must be ignored)", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page = s.do("GET", "/admin", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard: %d", resp.StatusCode)
	}
	// locale toggle persists on the user
	resp, _ = s.do("POST", "/admin/locale", url.Values{"_csrf": {csrfFrom(t, page)}, "locale": {"en"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("locale: %d", resp.StatusCode)
	}
	if _, page = s.do("GET", "/admin", nil); !strings.Contains(page, `dir="ltr"`) {
		t.Fatal("locale toggle not applied")
	}
	if u, _ := svc.GetByEmail(context.Background(), "ed@example.com"); u.Locale != "en" || u.LastLoginAt == "" {
		t.Fatalf("user not updated: %+v", u)
	}
	resp, _ = s.do("POST", "/admin/logout", url.Values{"_csrf": {csrfFrom(t, page)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/admin", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
}

func TestMagicLinkConfirmThenConsumeAndCustomerBlocked(t *testing.T) {
	s := boot(t)
	svc, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	u, _ := svc.Create(context.Background(), "c@example.com", "C", "", "customer")
	token, err := auth.NewMagicLink(context.Background(), s.app.Deps, u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, page := s.do("GET", "/auth/magic/"+token, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `action="/auth/magic/`+token+`"`) {
		t.Fatalf("GET must show a confirm form, not consume: %d", resp.StatusCode)
	}
	resp, page = s.do("GET", "/auth/magic/"+token, nil) // prefetchers may GET repeatedly
	if resp.StatusCode != 200 || !strings.Contains(page, `method="post"`) {
		t.Fatalf("second GET: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/auth/magic/"+token, url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		t.Fatalf("magic POST: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page = s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(page, "403") {
		t.Fatalf("customer in admin should get the 403 page: %d", resp.StatusCode)
	}
	if resp, _ := s.do("POST", "/auth/magic/"+token, url.Values{}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reused magic link: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/auth/magic/"+token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET of a used link should say invalid: %d", resp.StatusCode)
	}
}

func TestHTMXAnonymousGetsHXRedirect(t *testing.T) {
	s := boot(t)
	req, _ := http.NewRequest("POST", s.srv.URL+"/admin/content/1", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("HX-Redirect"), "/admin/login?next=") {
		t.Fatalf("htmx anonymous (expired session) POST: %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
}

func TestSessionAPIUnsafeMethodNeedsCSRFHeader(t *testing.T) {
	s := boot(t)
	svc, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	svc.Create(context.Background(), "ed@example.com", "Editor", "correct horse battery", "editor")
	_, page := s.do("GET", "/admin/login", nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"ed@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	resp, body := s.do("POST", "/api/v1/content", url.Values{})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, `"csrf_invalid"`) {
		t.Fatalf("session API POST without X-CSRF-Token: %d %s", resp.StatusCode, body)
	}
	if resp, _ := s.do("GET", "/api/v1/content", nil); resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Fatalf("session API GET should pass auth: %d", resp.StatusCode)
	}
}

func TestAdminCSRFFailureHTMXGetsEnvelope(t *testing.T) {
	s := boot(t)
	svc, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	svc.Create(context.Background(), "ed@example.com", "Editor", "correct horse battery", "editor")
	_, page := s.do("GET", "/admin/login", nil)
	s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"ed@example.com"}, "password": {"correct horse battery"}})
	req, _ := http.NewRequest("POST", s.srv.URL+"/admin/locale", strings.NewReader("locale=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	resp, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), `"csrf_invalid"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp2, page2 := s.do("POST", "/admin/locale", url.Values{"locale": {"en"}})
	if resp2.StatusCode != http.StatusForbidden || !strings.Contains(page2, "<html") {
		t.Fatalf("browser CSRF failure should render the 403 page: %d", resp2.StatusCode)
	}
}

func TestAPIAnonymousGets401Envelope(t *testing.T) {
	s := boot(t)
	resp, body := s.do("GET", "/api/v1/anything", nil)
	var env struct {
		OK    bool                  `json:"ok"`
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || resp.StatusCode != 401 || env.OK || env.Error.Code != "unauthorized" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestPermissionsDeclared(t *testing.T) {
	s := boot(t)
	names := map[string]bool{}
	for _, p := range s.app.Deps.Perms.All() {
		names[p.Name] = p.Label["ar"] != "" && p.Label["en"] != ""
	}
	for _, p := range []string{"content.view", "content.publish", "types.manage", "media.upload", "users.manage", "mcp.approve", "system.health", "commerce.view_orders"} {
		if !names[p] {
			t.Errorf("permission %s not declared with ar+en labels", p)
		}
	}
}
