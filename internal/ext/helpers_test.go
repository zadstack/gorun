package ext_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	hello "github.com/zadstack/gorun/examples/extensions/hello-http"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

type env struct {
	t       *testing.T
	app     *kernel.App
	svc     ext.Service
	content content.Service
	admin   *auth.User
	ctx     context.Context // acts as the admin user
}

// boot boots settings, auth, content, api, admin and ext with private
// networking allowed (bridge endpoints are httptest servers on 127.0.0.1).
func boot(t *testing.T) *env { t.Helper(); return bootAt(t, t.TempDir(), true) }

// bootAt boots on dataDir; withAdmin creates the admin user (first boot).
func bootAt(t *testing.T, dataDir string, withAdmin bool) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: dataDir, Addr: ":0", SecretKey: make([]byte, 32),
		BaseURL: "http://gorun.test", AllowPrivateNet: true}
	mods := []kernel.Module{settingsmod.New(), auth.New(), content.New(), api.New(), admin.New(), ext.New()}
	app, err := kernel.Boot(context.Background(), cfg, mods)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	t.Cleanup(app.Close)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	var u *auth.User
	if withAdmin {
		u, err = users.Create(context.Background(), "admin@example.com", "Admin", "correct horse battery staple", "admin")
	} else {
		u, err = users.GetByEmail(context.Background(), "admin@example.com")
	}
	if err != nil {
		t.Fatal(err)
	}
	svc, ok := kernel.Get[ext.Service](app.Deps, ext.ServiceName)
	if !ok {
		t.Fatal("ext service not registered")
	}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	return &env{t: t, app: app, svc: svc, content: cs, admin: u, ctx: auth.WithUser(context.Background(), u)}
}

// manifest returns a manifest map for slug delivering to endpoint.
func manifest(slug, endpoint string, hooks ...map[string]any) map[string]any {
	hs := make([]any, 0, len(hooks))
	for _, h := range hooks {
		hs = append(hs, h)
	}
	return map[string]any{
		"slug": slug, "name": map[string]string{"en": "Ext " + slug, "ar": "إضافة " + slug}, "version": "1.0.0",
		"permissions": []string{"content.view"}, "hooks": hs,
		"endpoint": map[string]string{"url": endpoint, "auth": "hmac"},
	}
}

func hook(name, kind string, timeoutMS int) map[string]any {
	return map[string]any{"name": name, "kind": kind, "timeout_ms": timeoutMS}
}

// writeManifest writes m as manifest.json into a fresh directory.
func writeManifest(t *testing.T, m map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *env) install(m map[string]any) *ext.InstallResult {
	e.t.Helper()
	res, err := e.svc.InstallDir(e.ctx, writeManifest(e.t, m))
	if err != nil {
		e.t.Fatalf("install: %v", err)
	}
	return res
}

// recorder wraps a handler and records request headers.
type recorder struct {
	mu      sync.Mutex
	next    http.Handler
	headers []http.Header
	bodies  [][]byte
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.headers = append(r.headers, req.Header.Clone())
	r.bodies = append(r.bodies, body)
	r.mu.Unlock()
	req.Body = io.NopCloser(bytes.NewReader(body))
	r.next.ServeHTTP(w, req)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.headers)
}

// helloServer runs the hello example behind httptest (secret set later).
func helloServer(t *testing.T) (*hello.Server, *recorder, string) {
	t.Helper()
	h := hello.Handler("unset")
	rec := &recorder{next: h}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return h, rec, srv.URL + "/gorun"
}

// eventually polls cond until it holds or 10s pass.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) status(slug string) *ext.Extension {
	e.t.Helper()
	x, err := e.svc.Status(context.Background(), slug)
	if err != nil {
		e.t.Fatalf("status %s: %v", slug, err)
	}
	return x
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "manifest.json"), raw, 0o644)
}
