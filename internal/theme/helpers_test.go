package theme

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme/fake"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

// lockedBuffer collects log output safely across goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type env struct {
	t    *testing.T
	app  *kernel.App
	svc  *service
	fake *fake.Module
	logs *lockedBuffer
	root string
	cfg  config.Config
}

// newEnv boots settings + auth + fake services + theme on root.
func newEnv(t *testing.T, root string, mutate ...func(*config.Config)) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	for _, m := range mutate {
		m(&cfg)
	}
	logs := &lockedBuffer{}
	fm := fake.NewModule()
	mod := New(WithThemesDir(root), WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fm, mod})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return &env{t: t, app: app, svc: mod.svc, fake: fm, logs: logs, root: root, cfg: cfg}
}

func (e *env) setting(key string, v any) {
	e.t.Helper()
	if err := e.app.Deps.Settings.Set(context.Background(), key, v); err != nil {
		e.t.Fatal(err)
	}
}

// render renders req for an anonymous visitor (or ctx when given).
func (e *env) render(req kernel.KindRequest, tc *Context, ctx ...context.Context) (*httptest.ResponseRecorder, error) {
	e.t.Helper()
	c := context.Background()
	if len(ctx) > 0 {
		c = ctx[0]
	}
	if tc == nil {
		tc = &Context{}
	}
	if tc.Request == nil {
		tc.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(c)
	}
	rec := httptest.NewRecorder()
	err := e.svc.Render(c, rec, req, tc)
	return rec, err
}

func adminCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "u1", Role: "admin", Status: auth.StatusActive, Name: "Admin"})
}

// writeTheme creates <root>/<slug> with theme.json and files (paths
// relative to the theme dir).
func writeTheme(t *testing.T, root, slug, themeJSON string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	if themeJSON == "" {
		themeJSON = `{"name":"` + slug + `","version":"1.0.0","locations":["primary","footer"]}`
	}
	files["theme.json"] = themeJSON
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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

// client is an HTTP client against the booted app with a cookie jar.
type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func (e *env) client() *client {
	e.t.Helper()
	srv := httptest.NewServer(e.app.Handler)
	e.t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, srv: srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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

// login creates a user with role and logs c in; returns the session CSRF token.
func (e *env) login(c *client, email, role string) string {
	e.t.Helper()
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), email, "User", "correct horse battery", role); err != nil {
		e.t.Fatal(err)
	}
	_, page := c.get("/admin/login")
	resp, _ := c.post("/admin/login", url.Values{"_csrf": {csrfFrom(e.t, page)}, "email": {email}, "password": {"correct horse battery"}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = c.get("/admin")
	m := regexp.MustCompile(`name="csrf-token" content="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf meta:\n%s", page)
	}
	return m[1]
}
