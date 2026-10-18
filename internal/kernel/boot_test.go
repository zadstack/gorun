package kernel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// fakeModule exercises the deferred-mount contract: it adds middleware to
// the Admin sub-router and then routes on every sub-router during Register.
type fakeModule struct {
	name     string
	migs     fstest.MapFS
	register func(d *kernel.Deps) error
}

func (m fakeModule) Name() string { return m.name }
func (m fakeModule) Migrations() fs.FS {
	if m.migs == nil {
		return nil
	}
	return m.migs
}
func (m fakeModule) Register(d *kernel.Deps) error {
	if m.register == nil {
		return nil
	}
	return m.register(d)
}

// catalogModule also implements kernel.CatalogProvider.
type catalogModule struct{ fakeModule }

func (catalogModule) Catalogs() fs.FS {
	return fstest.MapFS{"catalog/demo.en.json": {Data: []byte(`{"hi":"Hi"}`)}}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32)}
}

func get(t *testing.T, h http.Handler, method, path string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b), rec.Header()
}

func TestBootMountsSubRoutersAfterRegister(t *testing.T) {
	web := fakeModule{
		name: "content",
		migs: fstest.MapFS{"migrations/0001_c.sql": {Data: []byte("CREATE TABLE c(x);")}},
		register: func(d *kernel.Deps) error {
			d.Router.SetAuthenticator("fake")
			d.Router.Admin.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Admin-MW", "1")
					next.ServeHTTP(w, r)
				})
			})
			d.Router.Admin.Get("/x", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "admin") })
			d.Router.Public.Get("/", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "home") })
			d.Router.Public.Get("/{slug}", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "page") })
			d.Router.API.Get("/v1/ping", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "pong") })
			d.Router.Webhooks.Post("/stripe", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "hook") })
			if d.I18n.T("en", "demo.hi") != "Hi" {
				t.Error("module catalogs must be loaded before Register")
			}
			return nil
		},
	}
	app, err := kernel.Boot(context.Background(), testConfig(t),
		[]kernel.Module{settingsmod.New(), catalogModule{web}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	cases := []struct{ method, path, body string }{
		{"GET", "/admin/x", "admin"},
		{"GET", "/", "home"},
		{"GET", "/about", "page"},
		{"GET", "/api/v1/ping", "pong"},
		{"POST", "/webhooks/stripe", "hook"},
		{"GET", "/healthz", "ok"},
		{"GET", "/readyz", "ok"},
	}
	for _, c := range cases {
		code, body, hdr := get(t, app.Handler, c.method, c.path)
		if code != 200 || body != c.body {
			t.Errorf("%s %s: %d %q want %q", c.method, c.path, code, body, c.body)
		}
		if c.path == "/admin/x" && hdr.Get("X-Admin-MW") != "1" {
			t.Error("admin middleware not applied")
		}
	}
	if err := app.Deps.Settings.Set(context.Background(), "site.title", "x"); err != nil {
		t.Fatalf("settings module not migrated: %v", err)
	}
	if len(app.Deps.Perms.All()) == 0 {
		t.Fatal("settings module should declare settings.manage")
	}
	var n int
	if err := app.Deps.DB.QueryRow("SELECT count(*) FROM c").Scan(&n); err != nil {
		t.Fatalf("module migration not applied: %v", err)
	}
	if err := app.Deps.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatalf("jobs migration not applied: %v", err)
	}
	if err := app.Deps.DB.QueryRow("SELECT count(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatalf("audit migration not applied: %v", err)
	}
}

func TestBootRejectsBadModuleLists(t *testing.T) {
	cases := map[string][]kernel.Module{
		"out of BootOrder":     {settingsmod.New(), fakeModule{name: "content"}, fakeModule{name: "auth"}},
		"duplicate":            {settingsmod.New(), fakeModule{name: "content"}, fakeModule{name: "content"}},
		"reserved name":        {settingsmod.New(), fakeModule{name: "jobs"}},
		"reserved diagnostics": {settingsmod.New(), fakeModule{name: "diagnostics"}},
		"missing settings":     {fakeModule{name: "content"}},
	}
	for name, mods := range cases {
		if app, err := kernel.Boot(context.Background(), testConfig(t), mods); err == nil {
			app.Close()
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBootConvertsRegisterPanicToError(t *testing.T) {
	bad := fakeModule{name: "content", register: func(d *kernel.Deps) error {
		d.Admin.AddNav(kernel.NavItem{ID: "dup"})
		d.Admin.AddNav(kernel.NavItem{ID: "dup"}) // duplicate registration panics
		return nil
	}}
	_, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New(), bad})
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("want register error naming the module, got %v", err)
	}
}

// A later module may add middleware to a sub-router an earlier module
// already routed on (e.g. redirects after media); it covers those routes.
func TestLateUseCoversEarlierRoutes(t *testing.T) {
	media := fakeModule{name: "media", register: func(d *kernel.Deps) error {
		d.Router.Public.Get("/m", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "media") })
		return nil
	}}
	redirects := fakeModule{name: "redirects", register: func(d *kernel.Deps) error {
		d.Router.Public.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Order", w.Header().Get("X-Order")+"a")
				next.ServeHTTP(w, r)
			})
		})
		d.Router.Public.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Order", w.Header().Get("X-Order")+"b")
				next.ServeHTTP(w, r)
			})
		})
		return nil
	}}
	app, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New(), media, redirects})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	code, body, hdr := get(t, app.Handler, "GET", "/m")
	if code != 200 || body != "media" || hdr.Get("X-Order") != "ab" {
		t.Fatalf("%d %q X-Order=%q", code, body, hdr.Get("X-Order"))
	}
	if _, _, hdr := get(t, app.Handler, "GET", "/healthz"); hdr.Get("X-Order") != "" {
		t.Fatal("/healthz must bypass Public middleware")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("want panic for Use after the router started serving")
		}
	}()
	app.Deps.Router.Public.Use(func(h http.Handler) http.Handler { return h })
}

func TestAdminAndAPIDenyByDefaultWithoutAuthenticator(t *testing.T) {
	web := fakeModule{name: "content", register: func(d *kernel.Deps) error {
		d.Router.Admin.Get("/x", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "admin") })
		d.Router.API.Get("/v1/ping", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "pong") })
		d.Router.Public.Get("/", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "home") })
		return nil
	}}
	app, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New(), web})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, p := range []string{"/admin/x", "/admin/", "/api/v1/ping"} {
		code, body, _ := get(t, app.Handler, "GET", p)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "authentication module not installed") {
			t.Errorf("%s: %d %q", p, code, body)
		}
	}
	for p, want := range map[string]string{"/": "home", "/healthz": "ok", "/readyz": "ok"} {
		if code, body, _ := get(t, app.Handler, "GET", p); code != 200 || body != want {
			t.Errorf("%s: %d %q", p, code, body)
		}
	}
	if code, _, _ := get(t, app.Handler, "GET", "/metrics"); code != http.StatusNotFound {
		t.Fatalf("/metrics must be absent when GORUN_METRICS is off: %d", code)
	}
	app.Deps.Router.SetAuthenticator("auth")
	if code, body, _ := get(t, app.Handler, "GET", "/admin/x"); code != 200 || body != "admin" {
		t.Errorf("after SetAuthenticator: %d %q", code, body)
	}
}

func TestReadyzFailsWhenDBUnavailable(t *testing.T) {
	app, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	if code, _, _ := get(t, app.Handler, "GET", "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 after DB closed, got %d", code)
	}
}

func TestBootMetricsExport(t *testing.T) {
	cfg := testConfig(t)
	cfg.Metrics = true
	cfg.MetricsToken = "tok"
	web := fakeModule{name: "content", register: func(d *kernel.Deps) error {
		d.Router.SetAuthenticator("fake")
		d.Router.Public.Get("/", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "home") })
		return nil
	}}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), web})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, _, hdr := get(t, app.Handler, "GET", "/metrics"); hdr.Get("X-Order") != "" {
		t.Fatal("/metrics must bypass Public middleware")
	}
	code, _, _ := get(t, app.Handler, "GET", "/metrics")
	if code != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer tok")
	app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gorun_jobs") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if _, _, hdr := get(t, app.Handler, "GET", "/healthz"); hdr.Get("X-Order") != "" {
		t.Fatal("/healthz must still bypass Public middleware")
	}
}

func TestMigrateAllValidatesModules(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	bad := []kernel.Module{settingsmod.New(), fakeModule{name: "content"}, fakeModule{name: "auth"}}
	if err := kernel.MigrateAll(context.Background(), d, bad); err == nil {
		t.Fatal("want BootOrder error from MigrateAll")
	}
	if err := kernel.MigrateAll(context.Background(), d, []kernel.Module{settingsmod.New()}); err != nil {
		t.Fatal(err)
	}
}

// Streaming handlers (SSE, MCP streamable HTTP) type-assert http.Flusher;
// the root middleware must not hide it.
func TestRootMiddlewareKeepsFlusher(t *testing.T) {
	stream := fakeModule{name: "content", register: func(d *kernel.Deps) error {
		d.Router.Public.Get("/stream", func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flusher", http.StatusInternalServerError)
				return
			}
			io.WriteString(w, "chunk")
			f.Flush()
		})
		return nil
	}}
	app, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New(), stream})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app.Handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "chunk" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

func TestBootWithOptionsCanSkipJobWorkers(t *testing.T) {
	cfg := testConfig(t)
	var ran atomic.Int32
	probe := fakeModule{name: "probe", register: func(d *kernel.Deps) error {
		d.Jobs.Register("probe.run", func(context.Context, json.RawMessage) error { ran.Add(1); return nil }, 1)
		_, err := d.Jobs.Enqueue(context.Background(), "probe.run", nil)
		return err
	}}
	var logs bytes.Buffer
	kernel.LogOutput = &logs
	t.Cleanup(func() { kernel.LogOutput = io.Discard })
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), probe}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "jobs: workers not started") || strings.Contains(logs.String(), "jobs: workers started") {
		t.Fatalf("boot log must say no workers started:\n%s", logs.String())
	}
	// A job another (live) process is running must not be requeued as stale.
	app.Deps.DB.Exec(`INSERT INTO jobs (id, kind, payload_json, status, attempts, max_attempts, run_at, locked_by, locked_at, created_at, updated_at)
		VALUES ('live', 'probe.run', '{}', 'running', 0, 1, ?, 'server-w0', ?, ?, ?)`, db.Now(), db.Now(), db.Now(), db.Now())
	time.Sleep(1500 * time.Millisecond)
	var status string
	app.Deps.DB.QueryRow(`SELECT status FROM jobs WHERE id = 'live'`).Scan(&status)
	if ran.Load() != 0 || status != "running" {
		t.Fatalf("StartJobs=false ran %d jobs; live job status %q", ran.Load(), status)
	}
	app.Close()

	// The same data dir booted normally runs the queued job.
	app, err = kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), probe})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	deadline := time.Now().Add(5 * time.Second)
	for ran.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ran.Load() == 0 {
		t.Fatal("Boot must start the job workers")
	}
}

func TestBootDiagnosticsDoNotStartOnWireFailure(t *testing.T) {
	var captured *kernel.Deps
	bad := fakeModule{name: "content", register: func(d *kernel.Deps) error {
		captured = d
		d.Log.Error("content: synthetic boot failure")
		return errors.New("synthetic register failure")
	}}
	if app, err := kernel.Boot(context.Background(), testConfig(t), []kernel.Module{settingsmod.New(), bad}); err == nil {
		app.Close()
		t.Fatal("expected failed boot")
	}
	if captured == nil {
		t.Fatal("registration did not execute")
	}
	if captured.DB.Stats().OpenConnections != 0 {
		t.Fatal("failed boot retained DB connections")
	}
	c, ok := kernel.Get[*diagnostics.Collector](captured, diagnostics.ServiceName)
	if !ok {
		t.Fatal("missing collector")
	}
	time.Sleep(350 * time.Millisecond)
	if c.StorageFailures() != 0 {
		t.Fatal("collector started against closed failed-boot database")
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed-boot collector cleanup stalled")
	}
}
