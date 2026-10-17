// Package apitest boots GoRun for API tests and drives /api/v1 with a
// bearer API key. It is importable by any module's tests whose module comes
// after "api" in kernel.BootOrder (the import-order test also covers
// _test.go files).
//
// Set kernel.LogOutput = io.Discard in TestMain to keep test output clean.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// Boot boots GoRun on a temp data dir with the settings and auth modules
// followed by mods. Unless mods already contains a module named "api", the
// api module is inserted before the first module that comes after "api" in
// kernel.BootOrder (or appended). The app is closed when t ends.
func Boot(t testing.TB, mods ...kernel.Module) *kernel.App {
	t.Helper()
	list := []kernel.Module{settingsmod.New(), auth.New()}
	list = append(list, withAPI(mods)...)
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, list)
	if err != nil {
		t.Fatalf("apitest: boot: %v", err)
	}
	t.Cleanup(app.Close)
	return app
}

func withAPI(mods []kernel.Module) []kernel.Module {
	if slices.ContainsFunc(mods, func(m kernel.Module) bool { return m.Name() == "api" }) {
		return mods
	}
	apiPos := slices.Index(kernel.BootOrder, "api")
	at := slices.IndexFunc(mods, func(m kernel.Module) bool { return slices.Index(kernel.BootOrder, m.Name()) > apiPos })
	if at < 0 {
		at = len(mods)
	}
	return slices.Insert(slices.Clone(mods), at, kernel.Module(api.New()))
}

// Options configure New.
type Options struct {
	Role       string   // owner role, default "admin"
	Scopes     []string // key scopes, default ["*"]
	RatePerMin int      // key rate limit, default 100000 (effectively unlimited)
	Kind       string   // auth.KeyKindAPI (default) or auth.KeyKindAgent
}

// Client calls the API of a booted app with a bearer key.
type Client struct {
	t     testing.TB
	app   *kernel.App
	Token string       // plaintext bearer key
	User  *auth.User   // key owner
	Key   *auth.APIKey // the key
}

// NewClient returns a client whose key belongs to a fresh admin user and
// carries scopes (default "*").
func NewClient(t testing.TB, app *kernel.App, scopes ...string) *Client {
	t.Helper()
	return New(t, app, Options{Scopes: scopes})
}

// New returns a client for a fresh user and key configured by o.
func New(t testing.TB, app *kernel.App, o Options) *Client {
	t.Helper()
	if o.Role == "" {
		o.Role = "admin"
	}
	if len(o.Scopes) == 0 {
		o.Scopes = []string{"*"}
	}
	if o.RatePerMin <= 0 {
		o.RatePerMin = 100000
	}
	users, ok := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if !ok {
		t.Fatal("apitest: auth service not registered")
	}
	keys, ok := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	if !ok {
		t.Fatal("apitest: api key service not registered")
	}
	ctx := context.Background()
	email := "apitest-" + strings.ToLower(db.NewID()) + "@example.com"
	u, err := users.Create(ctx, email, "API Test", "correct horse battery staple", o.Role)
	if err != nil {
		t.Fatalf("apitest: create user: %v", err)
	}
	if o.Kind == "" {
		o.Kind = auth.KeyKindAPI
	}
	token, k, err := keys.CreateKind(ctx, o.Kind, "apitest", u.ID, o.Scopes, o.RatePerMin, "")
	if err != nil {
		t.Fatalf("apitest: create key: %v", err)
	}
	return &Client{t: t, app: app, Token: token, User: u, Key: k}
}

// URL maps a client path to the request path: "/api/…" is kept, "/v1/…"
// gets "/api" prepended, anything else gets "/api/v1" ("/content" →
// "/api/v1/content").
func URL(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/"):
		return path
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		return "/api" + path
	default:
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return "/api/v1" + path
	}
}

// Do sends a request with the bearer key (body: nil, []byte/string sent
// raw, anything else JSON-encoded) and returns the recorded response.
func (c *Client) Do(method, path string, body any, header http.Header) *httptest.ResponseRecorder {
	c.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	case string:
		r = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			c.t.Fatalf("apitest: encode body: %v", err)
		}
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, URL(path), r)
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, vs := range header {
		req.Header.Del(k)
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	c.app.Handler.ServeHTTP(w, req)
	return w
}

func (c *Client) call(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	w := c.Do(method, path, body, nil)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		c.t.Errorf("apitest: %s %s: response is not a JSON object (%d): %.200s", method, path, w.Code, w.Body.String())
	}
	return w.Code, out
}

// Get performs GET path and decodes the envelope.
func (c *Client) Get(path string) (status int, body map[string]any) {
	c.t.Helper()
	return c.call(http.MethodGet, path, nil)
}

// Post performs POST path with a JSON body.
func (c *Client) Post(path string, body any) (status int, resp map[string]any) {
	c.t.Helper()
	return c.call(http.MethodPost, path, body)
}

// Patch performs PATCH path with a JSON body.
func (c *Client) Patch(path string, body any) (status int, resp map[string]any) {
	c.t.Helper()
	return c.call(http.MethodPatch, path, body)
}

// Delete performs DELETE path (body may be nil).
func (c *Client) Delete(path string, body any) (status int, resp map[string]any) {
	c.t.Helper()
	return c.call(http.MethodDelete, path, body)
}

// Anonymous returns a copy of c that sends no credentials.
func (c *Client) Anonymous() *Client {
	cp := *c
	cp.Token = ""
	return &cp
}

// Data returns body["data"] as an object (nil if it is not one).
func Data(body map[string]any) map[string]any {
	m, _ := body["data"].(map[string]any)
	return m
}

// ErrorCode returns body["error"]["code"] ("" if absent).
func ErrorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
