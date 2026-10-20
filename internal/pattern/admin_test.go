package pattern

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

func TestAdminPatternsListAndActivate(t *testing.T) {
	themes := t.TempDir()
	writeTheme(t, themes, "default", `{"name":"Default","version":"1.0.0","supports":["rtl","identity"]}`, map[string]string{
		"templates/index.html": "INDEX",
	})
	patterns := filepath.Join("..", "..", "patterns")

	cfg := config.Config{
		Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32),
		BaseURL: "http://example.test", Theme: "default",
	}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{
		settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir(themes)),
		New(WithPatternsDir(patterns)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)

	c := newClient(t, app)
	if resp, _ := c.get("/admin/appearance/patterns"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	login(t, app, c, "admin@example.com", "admin")
	resp, page := c.get("/admin/appearance/patterns")
	if resp.StatusCode != 200 {
		t.Fatalf("patterns page: %d", resp.StatusCode)
	}
	for _, slug := range []string{"editorial", "company", "showcase", "guidebook"} {
		if !strings.Contains(page, slug) {
			t.Fatalf("missing slug %q in:\n%s", slug, page)
		}
		if !strings.Contains(page, `/admin/appearance/patterns/`+slug+`/activate`) {
			t.Fatalf("missing activate form for %s", slug)
		}
	}
	if !strings.Contains(page, "/admin/appearance/themes") {
		t.Fatal("missing link to theme identity picker")
	}

	token := csrfFrom(t, page)
	resp, _ = c.post("/admin/appearance/patterns/editorial/activate", url.Values{"_csrf": {token}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/appearance/patterns" {
		t.Fatalf("activate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	pat, ok := kernel.Get[Service](app.Deps, kernel.ServicePattern)
	if !ok {
		t.Fatal("pattern service missing")
	}
	info, active := pat.ActiveInfo(context.Background())
	if !active || info.Slug != "editorial" {
		t.Fatalf("active = %+v ok=%v", info, active)
	}

	_, page = c.get("/admin/appearance/patterns")
	token = csrfFrom(t, page)
	resp, _ = c.post("/admin/appearance/patterns/clear", url.Values{"_csrf": {token}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if _, still := pat.ActiveInfo(context.Background()); still {
		t.Fatal("pattern still active after clear")
	}
}

type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newClient(t *testing.T, app *kernel.App) *client {
	t.Helper()
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (c *client) do(method, path string, body io.Reader, hdr ...string) (*http.Response, string) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.srv.URL+path, body)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (c *client) get(path string) (*http.Response, string) {
	c.t.Helper()
	return c.do("GET", path, nil)
}

func (c *client) post(path string, form url.Values) (*http.Response, string) {
	c.t.Helper()
	return c.do("POST", path, strings.NewReader(form.Encode()), "Content-Type", "application/x-www-form-urlencoded")
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

func login(t *testing.T, app *kernel.App, c *client, email, role string) {
	t.Helper()
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), email, "User", "correct horse battery", role); err != nil {
		t.Fatal(err)
	}
	_, page := c.get("/admin/login")
	resp, _ := c.post("/admin/login", url.Values{
		"_csrf": {csrfFrom(t, page)}, "email": {email}, "password": {"correct horse battery"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
}
