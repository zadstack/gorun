package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/analytics"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

const (
	testBase  = "https://chat.example.com"
	testToken = "AbC123xyz789"
	sdkURL    = `"https://chat.example.com"`
	sdkPath   = "/packs/js/sdk.js"
)

type env struct {
	t   *testing.T
	app *kernel.App
	mod *Module
	srv *httptest.Server
}

// boot starts settings + auth + fake content + theme (repo themes/) + site +
// analytics + chat behind an httptest server, analytics before chat as in
// the CLI's module list.
func boot(t *testing.T) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://site.test", Theme: "default"}
	mod := New()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir("../../themes")), site.New(), analytics.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &env{t: t, app: app, mod: mod, srv: srv}
}

func (e *env) set(key string, v any) {
	e.t.Helper()
	if err := e.app.Deps.Settings.Set(context.Background(), key, v); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) configure() {
	e.set(SettingProvider, ProviderChatwoot)
	e.set(SettingBaseURL, testBase)
	e.set(SettingWebsiteToken, testToken)
}

func (e *env) get(path string) (*http.Response, string) {
	e.t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// directive returns the named CSP directive ("script-src 'self' …") or "".
func directive(csp, name string) string {
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if d == name || strings.HasPrefix(d, name+" ") {
			return d
		}
	}
	return ""
}

func TestChatInjectsOnThemePagesOnly(t *testing.T) {
	e := boot(t)
	e.configure()

	resp, body := e.get("/")
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if n := strings.Count(body, sdkPath); n != 1 {
		t.Fatalf("sdk loader %d times on a theme page, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, sdkURL) || !strings.Contains(body, `"`+testToken+`"`) || !strings.Contains(body, "window.chatwootSDK.run(") {
		t.Fatalf("loader lacks base url, token or run call:\n%s", body)
	}
	i, j := strings.Index(body, sdkPath), strings.LastIndex(strings.ToLower(body), "</body>")
	if j < 0 || i > j {
		t.Fatalf("loader at %d, </body> at %d: want it before </body>", i, j)
	}
	// The inline loader carries this response's CSP nonce.
	script := directive(resp.Header.Get("Content-Security-Policy"), "script-src")
	k := strings.Index(script, "'nonce-")
	if k < 0 {
		t.Fatalf("no nonce in %q", script)
	}
	nonce := strings.TrimSuffix(strings.Fields(script[k:])[0][len("'nonce-"):], "'")
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) {
		t.Fatalf("loader is not nonced with %q:\n%s", nonce, body)
	}
	// Analytics, in the same chain, still marks and injects theme pages.
	if !strings.Contains(body, analytics.ScriptPath) {
		t.Error("analytics script lost next to the chat loader")
	}

	if _, admin := e.get("/admin/x"); strings.Contains(admin, sdkPath) {
		t.Error("loader on an admin path")
	}
	if _, missing := e.get("/no-such-page"); strings.Contains(missing, sdkPath) {
		t.Error("loader on a 404")
	}

	// A JSON response, even one the theme marked, is never touched.
	h := e.mod.inject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.MarkThemePage(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"html":"</body>"}`)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/feed.json", nil))
	if strings.Contains(rec.Body.String(), sdkPath) {
		t.Errorf("loader in a JSON response: %s", rec.Body.String())
	}
	// A module's own HTML page (not theme-rendered) is never touched either.
	h = e.mod.inject(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><body>own page</body></html>`)
	}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/own", nil))
	if strings.Contains(rec.Body.String(), sdkPath) {
		t.Errorf("loader on a page the theme did not render: %s", rec.Body.String())
	}
}

func TestChatWidensCSPForItsHostOnly(t *testing.T) {
	e := boot(t)
	e.configure()

	resp, _ := e.get("/")
	csp := resp.Header.Get("Content-Security-Policy")
	if d := directive(csp, "script-src"); !hasSource(d, testBase) || !hasSource(d, "'self'") {
		t.Errorf("script-src %q lacks %s or 'self'", d, testBase)
	}
	if d := directive(csp, "connect-src"); !hasSource(d, testBase) || !hasSource(d, "wss://chat.example.com") || !hasSource(d, "'self'") {
		t.Errorf("connect-src %q lacks %s, its wss:// form or 'self'", d, testBase)
	}
	if d := directive(csp, "frame-src"); !hasSource(d, testBase) || !hasSource(d, "'self'") {
		t.Errorf("frame-src %q lacks %s or 'self'", d, testBase)
	}
	if n := strings.Count(csp, "frame-src"); n != 1 {
		t.Errorf("frame-src %d times: %q", n, csp)
	}
	for _, name := range []string{"default-src", "style-src", "img-src", "frame-ancestors", "object-src"} {
		if directive(csp, name) == "" {
			t.Errorf("widening dropped %s: %q", name, csp)
		}
	}
	for _, name := range []string{"script-src", "connect-src", "frame-src"} {
		d := directive(csp, name)
		for _, broad := range []string{"https:", "wss:", "*"} {
			if hasSource(d, broad) {
				t.Errorf("%s got the broad source %s: %q", name, broad, d)
			}
		}
	}

	// Pages without the widget keep the site CSP as it was.
	for _, p := range []string{"/no-such-page", "/admin/x"} {
		resp, _ := e.get(p)
		if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "chat.example.com") {
			t.Errorf("%s: CSP widened without the widget: %q", p, csp)
		}
	}

	// A stored base URL that is not a bare https origin (written by hand,
	// past the service) never reaches the CSP or the page.
	e.set(SettingBaseURL, "https://chat.example.com/x; script-src *")
	resp, body := e.get("/")
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "chat.example.com") || strings.Contains(body, sdkPath) {
		t.Errorf("an odd stored base URL was used: %q", csp)
	}
}

func TestChatOffRemovesWidget(t *testing.T) {
	e := boot(t)
	e.configure()
	if _, body := e.get("/"); !strings.Contains(body, sdkPath) {
		t.Fatal("control: no loader while configured")
	}

	e.set(SettingProvider, "")
	resp, body := e.get("/")
	if strings.Contains(body, sdkPath) || strings.Contains(resp.Header.Get("Content-Security-Policy"), "chat.example.com") {
		t.Error("loader or CSP source with an empty provider")
	}

	e.configure()
	if err := e.app.Deps.Settings.DeletePrefix(context.Background(), "chat."); err != nil {
		t.Fatal(err)
	}
	resp, body = e.get("/")
	if strings.Contains(body, sdkPath) || strings.Contains(resp.Header.Get("Content-Security-Policy"), "chat.example.com") {
		t.Error("loader or CSP source after the settings were removed")
	}

	e.configure()
	e.set(SettingWebsiteToken, "")
	if _, body := e.get("/"); strings.Contains(body, sdkPath) {
		t.Error("loader without a website token")
	}
}
