package seo_test

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

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

type site struct {
	t    *testing.T
	srv  *httptest.Server
	app  *kernel.App
	c    *http.Client
	csrf string
}

func boot(t *testing.T, role string, extra ...kernel.Module) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, append(append([]kernel.Module{settingsmod.New(), auth.New()}, extra...), media.New(), seo.New()))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, srv: srv, app: app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "u@example.com", "U", "correct horse battery", role); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	s.csrf = csrfRe.FindStringSubmatch(page)[1]
	if resp, _ := s.do("POST", "/admin/login", url.Values{"email": {"u@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil)
	s.csrf = csrfRe.FindStringSubmatch(page)[1]
	return s
}

func (s *site) do(method, path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		form.Set("_csrf", s.csrf)
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

func TestSEOSettingsPage(t *testing.T) {
	s := boot(t, "admin")
	resp, page := s.do("GET", "/admin/settings/seo", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `value="{title} – {site}"`) || !strings.Contains(page, "gp-media-picker-body") ||
		!strings.Contains(page, `href="/admin/settings/seo"`) || !strings.Contains(page, "Sitemap: http://localhost/sitemap.xml") {
		t.Fatalf("form: %d\n%s", resp.StatusCode, page)
	}
	if resp, page := s.do("POST", "/admin/settings/seo", url.Values{"title_template": {"no placeholder"}, "default_og_image": {"//evil"}}); resp.StatusCode != http.StatusUnprocessableEntity ||
		strings.Count(page, "field-error") != 2 {
		t.Fatalf("invalid: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/admin/settings/seo", url.Values{"title_template": {"{title} | {site}"}, "default_description": {"Desc"},
		"default_og_image": {"/media/2026/10/x.png"}, "robots_txt": {"User-agent: *\r\nDisallow: /tmp"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	svc, _ := kernel.Get[seo.Service](s.app.Deps, kernel.ServiceSEO)
	if got := svc.Robots(context.Background()); got != "User-agent: *\nDisallow: /tmp\n" {
		t.Fatalf("robots: %q", got)
	}
	if out := string(svc.Render(context.Background(), seo.Meta{}, seo.Meta{Title: "Page"})); !strings.Contains(out, `content="Desc"`) ||
		!strings.Contains(out, "http://localhost/media/2026/10/x.png") {
		t.Fatalf("render after save:\n%s", out)
	}
	if entries, _, _ := s.app.Deps.Audit.List(context.Background(), audit.ListQuery{Action: "seo.settings_update"}); len(entries) != 1 {
		t.Fatalf("audit: %+v", entries)
	}
}

func TestSEOSettingsRequirePermission(t *testing.T) {
	s := boot(t, "editor")
	if resp, _ := s.do("GET", "/admin/settings/seo", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}
