package site_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

type env struct {
	t    *testing.T
	app  *kernel.App
	srv  *httptest.Server
	c    *http.Client
	fake *fake.Module
}

// boot starts settings + auth + fake services + theme (repo themes/) + site
// (+ extra modules, e.g. a fake mailer).
func boot(t *testing.T, extra ...kernel.Module) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	fm := fake.NewModule()
	app, err := kernel.Boot(context.Background(), cfg, append([]kernel.Module{settingsmod.New(), auth.New(), fm,
		theme.New(theme.WithThemesDir("../../themes")), site.New()}, extra...))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &env{t: t, app: app, srv: srv, c: c, fake: fm}
}

func (e *env) do(method, path string, form url.Values, hdr ...string) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
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

func (e *env) get(path string) (*http.Response, string) { e.t.Helper(); return e.do("GET", path, nil) }

func (e *env) setting(key string, v any) {
	e.t.Helper()
	if err := e.app.Deps.Settings.Set(context.Background(), key, v); err != nil {
		e.t.Fatal(err)
	}
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

// loginAdmin creates an admin and logs the client in.
func (e *env) loginAdmin() {
	e.t.Helper()
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "admin@example.com", "Admin", "correct horse battery", "admin"); err != nil {
		e.t.Fatal(err)
	}
	_, page := e.get("/admin/login")
	resp, _ := e.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(e.t, page)}, "email": {"admin@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: %d", resp.StatusCode)
	}
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("body missing %q:\n%s", w, body)
		}
	}
}

func mustNotContain(t *testing.T, body string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(body, b) {
			t.Fatalf("body must not contain %q:\n%s", b, body)
		}
	}
}
