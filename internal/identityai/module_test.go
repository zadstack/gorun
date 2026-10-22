package identityai

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
)

const testPage = "/admin/appearance/identity/ai"

type fakeProvider struct {
	connections   []Connection
	writing       Connection
	ready         bool
	reply         string
	err           error
	calls         int
	lastSystem    string
	lastInput     string
	lastModel     string
	lastMaxTokens int
	fingerprint   string // changes when the destination's config or key changes
}

func (f *fakeProvider) hooks() Hooks {
	return Hooks{
		Complete: func(_ context.Context, _ *kernel.Deps, _, model, system, input string, maxTokens int, _ string) (string, error) {
			f.calls++
			f.lastSystem, f.lastInput, f.lastModel, f.lastMaxTokens = system, input, model, maxTokens
			return f.reply, f.err
		},
		Ready:       func(context.Context, *kernel.Deps, string, string) bool { return f.ready },
		Connections: func(context.Context, *kernel.Deps) ([]Connection, error) { return f.connections, nil },
		Writing: func(context.Context, *kernel.Deps) (Connection, bool) {
			return f.writing, f.ready && f.writing.Provider != ""
		},
		Fingerprint: func(_ context.Context, _ *kernel.Deps, id string) (string, bool) {
			return id + ":" + f.fingerprint, f.ready
		},
	}
}

type env struct {
	t      *testing.T
	app    *kernel.App
	srv    *httptest.Server
	c      *http.Client
	fp     *fakeProvider
	themes string
}

func boot(t *testing.T, enabled bool, fp *fakeProvider) *env {
	t.Helper()
	root := copyThemes(t)
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "default"}
	pre, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.Deps.Settings.Set(context.Background(), SettingEnabled, enabled); err != nil {
		t.Fatal(err)
	}
	pre.Close()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), theme.New(theme.WithThemesDir(root), theme.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))), New(fp.hooks())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, app: app, srv: srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, fp: fp, themes: root}
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "admin@example.com", "Admin", "correct horse battery", "admin"); err != nil {
		t.Fatal(err)
	}
	_, page := e.get("/admin/login")
	e.post("/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"admin@example.com"}, "password": {"correct horse battery"}})
	return e
}

// useTheme activates a child theme that declares no identity support.
func (e *env) useTheme(t *testing.T, slug string) {
	t.Helper()
	dir := filepath.Join(e.themes, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"Plain child","version":"1.0.0","parent":"default","locations":["primary"],"supports":["rtl"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Deps.Settings.Set(context.Background(), "site.theme", slug); err != nil {
		t.Fatal(err)
	}
}

// copyThemes copies the catalog themes to a temp dir (tests add themes).
func copyThemes(t *testing.T) string {
	t.Helper()
	src, _ := filepath.Abs("../../themes")
	dst := t.TempDir()
	if out, err := exec.Command("cp", "-R", src+"/.", dst).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	return dst
}

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	m := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(html)
	if m == nil {
		m = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`).FindStringSubmatch(html)
	}
	if m == nil {
		t.Fatal("no csrf token")
	}
	return m[1]
}

func (e *env) get(path string, hdr ...string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) post(path string, form url.Values) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "en")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) csrf() string {
	e.t.Helper()
	_, page := e.get(testPage)
	return csrfFrom(e.t, page)
}

func adminCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "u1", Role: "admin", Status: auth.StatusActive})
}

const goodReply = `{"accent":"#c2410c","background":"#fffdf8","text":"#1b1b1f","heading_font":"readex","body_font":"ibm-plex-arabic","latin_font":"inter","radius":"soft","tagline_ar":"متجر للقهوة المختصة","tagline_en":"Specialty coffee, roasted weekly","logo_brief":"A wordmark in a warm serif with a small bean mark; keep it flat and single-colour."}`

func TestModuleSwitchedOffHidesPageAndNav(t *testing.T) {
	e := boot(t, false, &fakeProvider{})
	if resp, _ := e.get(testPage); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled module page: %d", resp.StatusCode)
	}
	if _, settings := e.get("/admin/appearance/theme-settings"); strings.Contains(settings, `data-nav-id="appearance.identity_ai"`) {
		t.Fatal("disabled module registered navigation")
	}
}

func TestModuleEnabledShowsPageWithHonestScope(t *testing.T) {
	e := boot(t, true, &fakeProvider{ready: true, connections: []Connection{{ID: "c1", Name: "Anthropic main", Provider: "anthropic", Model: "claude-sonnet-4-5", Host: "api.anthropic.com"}}})
	resp, page := e.get(testPage, "Accept-Language", "en")
	if resp.StatusCode != 200 {
		t.Fatalf("page: %d", resp.StatusCode)
	}
	for _, want := range []string{"No images, vectors or GIFs are generated", `name="connection"`, "Anthropic main", `name="action" value="preview"`, "/admin/appearance/identity"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(page, `data-nav-id="appearance.identity_ai"`) {
		t.Fatal("enabled module missing from navigation")
	}
	_, ar := e.get(testPage, "Accept-Language", "ar")
	if !strings.Contains(ar, "لا يُنتج") {
		t.Error("arabic scope statement missing")
	}
}

func TestPreviewWithoutProviderExplainsAndMakesNoCall(t *testing.T) {
	fp := &fakeProvider{ready: false, reply: goodReply}
	e := boot(t, true, fp)
	resp, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "sector": {"coffee"}, "tone": {"warm"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "No AI connection is ready") {
		t.Fatalf("preview without provider: %d", resp.StatusCode)
	}
	if fp.calls != 0 {
		t.Fatal("provider was called")
	}
	if _, ok := themeSvc(e).IdentityDraft(adminCtx(), "default"); ok {
		t.Fatal("draft created")
	}
}

func themeSvc(e *env) IdentityStore {
	s, _ := kernel.Get[IdentityStore](e.app.Deps, kernel.ServiceTheme)
	return s
}

func TestPreviewShowsPromptCostAndRequiresApproval(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, connections: []Connection{{ID: "c1", Name: "OpenAI", Provider: "openai", Model: "gpt-4o-mini", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	resp, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"specialty coffee"}, "tone": {"warm"}})
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	for _, want := range []string{"gpt-4o-mini", "api.openai.com", "specialty coffee", `name="review"`, "Estimated cost", "$", `name="approved"`, `name="action" value="generate"`, "price list"} {
		if !strings.Contains(page, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	if fp.calls != 0 {
		t.Fatal("preview called the provider")
	}
	review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	// Generate without the approval box: refused, no call.
	resp, _ = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}})
	if resp.StatusCode != http.StatusUnprocessableEntity || fp.calls != 0 {
		t.Fatalf("unapproved generate: %d calls=%d", resp.StatusCode, fp.calls)
	}
	// Approved: one call, result written to the identity draft only.
	resp, page = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != 200 || fp.calls != 1 {
		t.Fatalf("generate: %d calls=%d", resp.StatusCode, fp.calls)
	}
	for _, want := range []string{"#c2410c", "Readex Pro", "Specialty coffee, roasted weekly", "متجر للقهوة المختصة", "wordmark", "saved as a draft"} {
		if !strings.Contains(page, want) {
			t.Errorf("result missing %q", want)
		}
	}
	if !strings.Contains(fp.lastInput, "specialty coffee") || !strings.Contains(fp.lastSystem, "JSON") {
		t.Errorf("prompt not built from the brief: %q", fp.lastInput)
	}
	d, ok := themeSvc(e).IdentityDraft(adminCtx(), "default")
	if !ok || d.Snapshot.Values["identity_accent"] != "#c2410c" || d.Snapshot.Values["identity_latin_font"] != "inter" || d.Snapshot.Site["site.tagline"] != "متجر للقهوة المختصة" {
		t.Fatalf("draft = %+v ok=%v", d.Snapshot, ok)
	}
	if got := e.app.Deps.Settings.GetString(adminCtx(), "theme.default.identity_accent", ""); got != "" {
		t.Fatal("result applied live")
	}
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'identityai.generate'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	// The review token is single-use.
	resp, _ = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != http.StatusConflict || fp.calls != 1 {
		t.Fatalf("reused token: %d calls=%d", resp.StatusCode, fp.calls)
	}
}

func TestGenerateFailuresLeaveDraftUntouched(t *testing.T) {
	for name, fp := range map[string]*fakeProvider{
		"upstream":  {ready: true, err: errors.New("boom")},
		"not json":  {ready: true, reply: "Here is a palette: orange and cream."},
		"bad color": {ready: true, reply: `{"accent":"orange","background":"#fff","text":"#111","tagline_en":"x"}`},
		"bad font":  {ready: true, reply: `{"accent":"#c2410c","background":"#ffffff","text":"#111111","heading_font":"comic","tagline_en":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			fp.connections = []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}
			e := boot(t, true, fp)
			if err := themeSvc(e).SaveIdentityDraft(adminCtx(), theme.IdentitySnapshot{Theme: "default", Values: map[string]string{"identity_accent": "#123456"}, Site: map[string]string{"site.title": "Keep"}}); err != nil {
				t.Fatal(err)
			}
			_, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}})
			review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
			resp, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
			if resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if !strings.Contains(page, "data-identityai-error") || strings.Contains(page, "boom") {
				t.Fatalf("error not shown safely: %d", resp.StatusCode)
			}
			d, ok := themeSvc(e).IdentityDraft(adminCtx(), "default")
			if !ok || d.Snapshot.Values["identity_accent"] != "#123456" {
				t.Fatalf("draft changed: %+v ok=%v", d.Snapshot, ok)
			}
		})
	}
}

func TestEstimateCostKnownAndUnknownModels(t *testing.T) {
	known := EstimateCost("anthropic", "claude-sonnet-4-5", 1200, 1500)
	if !known.Known || known.USD <= 0 || known.AsOf == "" {
		t.Fatalf("known model estimate = %+v", known)
	}
	unknown := EstimateCost("openai", "my-custom-model", 1200, 1500)
	if unknown.Known || unknown.USD != 0 {
		t.Fatalf("unknown model estimate = %+v", unknown)
	}
}

// Review fixes: the destination shown is the one called, output is capped
// to the estimate, prices match exact or dated model ids only, the test
// status is visible, unsupported themes are refused, and every provider
// call is audited with its outcome.
func TestPricingMatchesExactOrDatedModelsOnly(t *testing.T) {
	if e := EstimateCost("openai", "gpt-4o", 1000, 1000); !e.Known {
		t.Fatal("exact gpt-4o unknown")
	}
	if e := EstimateCost("openai", "gpt-4o-realtime", 1000, 1000); e.Known {
		t.Fatalf("gpt-4o-realtime must not inherit gpt-4o's price: %+v", e)
	}
	if e := EstimateCost("anthropic", "claude-sonnet-4-5-20250929", 1000, 1000); !e.Known {
		t.Fatal("dated sonnet id unknown")
	}
	if e := EstimateCost("openai", "gpt-4.1-nano", 1000, 1000); !e.Known || e.PerMillionInput >= EstimateCost("openai", "gpt-4.1", 1, 1).PerMillionInput {
		t.Fatalf("gpt-4.1-nano must have its own, lower price: %+v", e)
	}
}

func TestPreviewShowsWritingSettingsDestinationAndTestStatus(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, writing: Connection{Provider: "openai", Model: "gpt-4o-mini", Host: "api.openai.com"},
		connections: []Connection{{ID: "c1", Name: "Proxy", Provider: "anthropic", Model: "claude-sonnet-4-5", Host: "proxy.example.com", TestStatus: "pass"}}}
	e := boot(t, true, fp)
	_, page := e.get(testPage, "Accept-Language", "en")
	mustContain(t, page, "Proxy · anthropic · claude-sonnet-4-5 · pass")
	_, page = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "sector": {"x"}})
	mustContain(t, page, "gpt-4o-mini", "api.openai.com", "Estimated cost", "$")
	review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if fp.lastModel != "gpt-4o-mini" || fp.lastMaxTokens != maxOutputTokens {
		t.Fatalf("call used model %q max %d", fp.lastModel, fp.lastMaxTokens)
	}
}

func TestGenerateRefusedForThemeWithoutIdentitySupport(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, connections: []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	e.useTheme(t, "plainchild")
	resp, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "does not accept identity settings") || fp.calls != 0 {
		t.Fatalf("unsupported theme: %d calls=%d", resp.StatusCode, fp.calls)
	}
}

func TestEveryProviderCallIsAuditedAndStrayBracesParse(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply + " Note: replace {brand} later.", connections: []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	_, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}, "languages": {"en"}})
	review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	resp, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != 200 {
		t.Fatalf("stray braces after the object broke parsing: %d", resp.StatusCode)
	}
	mustContain(t, page, "not saved", "Specialty coffee, roasted weekly")
	d, _ := themeSvc(e).IdentityDraft(adminCtx(), "default")
	if d.Snapshot.Site["site.tagline"] != "Specialty coffee, roasted weekly" {
		t.Fatalf("english choice not honoured: %q", d.Snapshot.Site["site.tagline"])
	}
	fp.reply = "not json at all"
	_, page = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}})
	review = regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'identityai.generate'`).Scan(&n)
	if n != 2 {
		t.Fatalf("audit rows = %d, want one per provider call", n)
	}
	var meta string
	_ = e.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'identityai.generate' ORDER BY rowid DESC LIMIT 1`).Scan(&meta)
	if !strings.Contains(meta, `"outcome":"bad_reply"`) {
		t.Fatalf("last audit meta = %s", meta)
	}
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

// The approval binds to the exact destination reviewed: when the saved
// connection (endpoint, provider, model or key) or the writing settings
// change after Preview, or the active theme changes, Generate refuses
// without a provider call, a draft write or an audit row.
func TestGenerateRefusedWhenDestinationChangedSincePreview(t *testing.T) {
	for _, path := range []string{"connection", "writing"} {
		t.Run(path, func(t *testing.T) {
			fp := &fakeProvider{ready: true, reply: goodReply, fingerprint: "v1", writing: Connection{Provider: "openai", Model: "gpt-4o-mini", Host: "api.openai.com"},
				connections: []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}}
			e := boot(t, true, fp)
			form := url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "sector": {"x"}}
			if path == "connection" {
				form.Set("connection", "c1")
			}
			_, page := e.post(testPage, form)
			review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
			fp.fingerprint = "v2" // endpoint, model or key edited after the preview
			resp, body := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
			if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "changed since the preview") || fp.calls != 0 {
				t.Fatalf("changed destination: %d calls=%d", resp.StatusCode, fp.calls)
			}
			if _, ok := themeSvc(e).IdentityDraft(adminCtx(), "default"); ok {
				t.Fatal("draft written")
			}
			var n int
			_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'identityai.%'`).Scan(&n)
			if n != 0 {
				t.Fatalf("audit rows = %d", n)
			}
			// The stale review is gone: a second attempt is a plain review_required.
			if resp, _ := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}}); resp.StatusCode != http.StatusConflict || fp.calls != 0 {
				t.Fatalf("stale review reused: %d", resp.StatusCode)
			}
		})
	}
}

func TestGenerateRefusedWhenThemeChangedSincePreview(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, fingerprint: "v1", connections: []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	_, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}})
	review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	if err := e.app.Deps.Settings.Set(context.Background(), "site.theme", "business"); err != nil {
		t.Fatal(err)
	}
	resp, _ := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != http.StatusConflict || fp.calls != 0 {
		t.Fatalf("changed theme: %d calls=%d", resp.StatusCode, fp.calls)
	}
	for _, slug := range []string{"default", "business"} {
		if _, ok := themeSvc(e).IdentityDraft(adminCtx(), slug); ok {
			t.Fatalf("%s draft written", slug)
		}
	}
}

// A review token belongs to the admin who previewed: another admin cannot
// spend it.
func TestReviewTokenBoundToOwner(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, fingerprint: "v1", connections: []Connection{{ID: "c1", Name: "P", Provider: "openai", Model: "gpt-4o", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	_, page := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {"c1"}, "sector": {"x"}})
	review := regexp.MustCompile(`name="review" value="([^"]+)"`).FindStringSubmatch(page)[1]
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "second@example.com", "Second", "correct horse battery", "admin"); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	other := &env{t: t, app: e.app, srv: e.srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, fp: fp}
	_, login := other.get("/admin/login")
	other.post("/admin/login", url.Values{"_csrf": {csrfFrom(t, login)}, "email": {"second@example.com"}, "password": {"correct horse battery"}})
	resp, _ := other.post(testPage, url.Values{"_csrf": {other.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != http.StatusConflict || fp.calls != 0 {
		t.Fatalf("another admin spent the token: %d calls=%d", resp.StatusCode, fp.calls)
	}
	// The owner can still use it.
	resp, _ = e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"generate"}, "review": {review}, "approved": {"1"}})
	if resp.StatusCode != 200 || fp.calls != 1 {
		t.Fatalf("owner refused: %d calls=%d", resp.StatusCode, fp.calls)
	}
}

func TestPricingCitesSourceAndSkipsReasoningModels(t *testing.T) {
	if e := EstimateCost("anthropic", "claude-sonnet-5-5", 1000, 1000); !e.Known || e.Source == "" || e.PerMillionInput != 2 {
		t.Fatalf("sonnet 5.5 = %+v", e)
	}
	for _, m := range []string{"gpt-5", "gpt-5-mini", "o3", "o4-mini"} {
		if e := EstimateCost("openai", m, 1, 1); e.Known {
			t.Fatalf("%s must be unpriced (client sends max_tokens, which reasoning models reject)", m)
		}
	}
}

// Models the shared client cannot drive (OpenAI reasoning models need
// max_completion_tokens) are refused at preview, before any approval.
func TestPreviewRefusesModelsTheClientCannotDrive(t *testing.T) {
	fp := &fakeProvider{ready: true, reply: goodReply, fingerprint: "v1", writing: Connection{Provider: "openai", Model: "o3", Host: "api.openai.com"},
		connections: []Connection{{ID: "c1", Name: "Reasoning", Provider: "openai", Model: "gpt-5-mini", Host: "api.openai.com"}}}
	e := boot(t, true, fp)
	_, page := e.get(testPage, "Accept-Language", "en")
	mustContain(t, page, "gpt-5-mini · not supported here")
	for _, conn := range []string{"c1", ""} {
		resp, body := e.post(testPage, url.Values{"_csrf": {e.csrf()}, "action": {"preview"}, "connection": {conn}, "sector": {"x"}})
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "cannot be used from here") || strings.Contains(body, `name="review"`) {
			t.Fatalf("connection %q: %d", conn, resp.StatusCode)
		}
	}
	if fp.calls != 0 {
		t.Fatal("provider called")
	}
}
