package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

const testPassword = "correct horse battery staple"

// fakeModule registers a counting sensitive tool and optional fake
// commerce / wpmigrate services, like modules booted before mcp would.
type fakeModule struct {
	calls     atomic.Int32
	commerce  bool
	wpmigrate bool
}

func (*fakeModule) Name() string { return "mcpfake" }

func (*fakeModule) Migrations() fs.FS { return nil }

func (f *fakeModule) Register(d *kernel.Deps) error {
	d.MCP.AddTool(kernel.MCPTool{
		Name: "fake_sensitive", Description: "counts executions", Scopes: []string{"content.view"}, Sensitive: true,
		Input: struct {
			Note string `json:"note,omitempty"`
		}{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			n := f.calls.Add(1)
			return map[string]any{"executed": n, "actor": auth.ActorFromContext(ctx).Kind}, nil
		},
	})
	if f.commerce {
		d.Services.Set(kernel.ServiceCommerce, &fakeCommerce{})
	}
	if f.wpmigrate {
		d.Services.Set(serviceWPMigrate, &fakeMigrator{})
	}
	return nil
}

type fakeProduct struct {
	Slug       string `json:"slug"`
	PriceMinor int64  `json:"price_minor"`
}

// fakeCommerce mirrors the commerce.Service method shape the tools use.
type fakeCommerce struct{}

func (*fakeCommerce) Products(ctx context.Context, activeOnly bool) ([]fakeProduct, error) {
	return []fakeProduct{{Slug: "pro", PriceMinor: 9900}}, nil
}

type fakeReport struct {
	MigrationID string `json:"migration_id"`
	Status      string `json:"status"`
}

// fakeMigrator mirrors the wpmigrate.Service method shape the tools use.
type fakeMigrator struct{}

func (*fakeMigrator) Preview(ctx context.Context, id string) (*fakeReport, error) {
	return &fakeReport{MigrationID: id, Status: "previewed"}, nil
}

func (*fakeMigrator) Status(ctx context.Context, id string) (*fakeReport, error) {
	return &fakeReport{MigrationID: id, Status: "running"}, nil
}

type env struct {
	t    *testing.T
	app  *kernel.App
	srv  *httptest.Server
	fake *fakeModule
	web  *http.Client // admin browser (cookie jar)
}

func newEnv(t *testing.T, fake *fakeModule) *env {
	t.Helper()
	if fake == nil {
		fake = &fakeModule{}
	}
	app := apitest.Boot(t, content.New(), taxonomy.New(), media.New(), menus.New(),
		theme.New(theme.WithThemesDir("../../themes")), admin.New(), fake, New())
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	return &env{t: t, app: app, srv: srv, fake: fake}
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// agent is an MCP client session authenticated with an API key.
type agent struct {
	t   *testing.T
	cs  *sdk.ClientSession
	key *apitest.Client
}

func (e *env) agent(scopes ...string) *agent {
	e.t.Helper()
	key := apitest.NewClient(e.t, e.app, scopes...)
	return e.agentFor(key)
}

func (e *env) agentFor(key *apitest.Client) *agent {
	e.t.Helper()
	tr := &sdk.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearer{token: key.Token, base: http.DefaultTransport}}}
	c := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "v0"}, nil)
	cs, err := c.Connect(context.Background(), tr, nil)
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return &agent{t: e.t, cs: cs, key: key}
}

func (a *agent) toolNames() []string {
	a.t.Helper()
	res, err := a.cs.ListTools(context.Background(), nil)
	if err != nil {
		a.t.Fatalf("list tools: %v", err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	return out
}

// call invokes a tool and returns the decoded envelope and IsError.
func (a *agent) call(name string, args any) (map[string]any, bool) {
	a.t.Helper()
	res, err := a.cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("call %s: %v", name, err)
	}
	if len(res.Content) == 0 {
		a.t.Fatalf("call %s: empty content", name)
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		a.t.Fatalf("call %s: content is %T", name, res.Content[0])
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &env); err != nil {
		a.t.Fatalf("call %s: not JSON: %s", name, tc.Text)
	}
	return env, res.IsError
}

// ok calls a tool that must succeed and returns its data object.
func (a *agent) ok(name string, args any) map[string]any {
	a.t.Helper()
	env, isErr := a.call(name, args)
	if isErr || env["ok"] != true {
		a.t.Fatalf("call %s: want ok, got %v", name, env)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

// fails calls a tool that must fail and returns the error object.
func (a *agent) fails(name string, args any) map[string]any {
	a.t.Helper()
	env, isErr := a.call(name, args)
	if !isErr || env["ok"] != false {
		a.t.Fatalf("call %s: want error, got %v", name, env)
	}
	e, _ := env["error"].(map[string]any)
	return e
}

// login signs an admin in with a fresh cookie jar.
func (e *env) login(role string) *auth.User {
	e.t.Helper()
	users, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	email := role + "-" + strings.ToLower(randomSuffix()) + "@example.com"
	u, err := users.Create(context.Background(), email, "Admin", testPassword, role)
	if err != nil {
		e.t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	e.web = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, page := e.do("GET", "/admin/login", nil)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf on login page")
	}
	resp, _ := e.do("POST", "/admin/login", url.Values{"_csrf": {m[1]}, "email": {email}, "password": {testPassword}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login: %d", resp.StatusCode)
	}
	return u
}

var (
	csrfFieldRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	csrfMetaRe  = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
)

func randomSuffix() string { return strings.ReplaceAll(strings.ToLower(db.NewID()), "-", "") }

func (e *env) do(method, path string, form url.Values, headers ...string) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := e.web.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (e *env) csrf() string {
	e.t.Helper()
	_, page := e.do("GET", "/admin/agents/approvals", nil)
	m := csrfMetaRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no csrf meta on approvals page:\n%.500s", page)
	}
	return m[1]
}

// adminPost posts a form with the session CSRF token as an HTMX request.
func (e *env) adminPost(path string) (*http.Response, string) {
	e.t.Helper()
	return e.do("POST", path, url.Values{"_csrf": {e.csrf()}}, "HX-Request", "true")
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.app.Deps.DB.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
