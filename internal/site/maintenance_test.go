package site_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestMaintenanceMode(t *testing.T) {
	root := t.TempDir()
	for p, body := range map[string]string{"theme.json": `{"name":"Mini"}`, "templates/index.html": `INDEX`, "assets/a.css": `a{}`} {
		full := filepath.Join(root, "mini", filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "mini"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(), theme.New(theme.WithThemesDir(root)), site.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if code, body, _ := get("/"); code != 200 || !strings.Contains(body, "INDEX") {
		t.Fatalf("before: %d %s", code, body)
	}
	ctx := context.Background()
	app.Deps.Settings.Set(ctx, "site.title", "زاد")
	app.Deps.Settings.Set(ctx, site.SettingMaintenance, true)
	code, body, h := get("/")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "تحت الصيانة") || !strings.Contains(body, "زاد") || h.Get("Retry-After") == "" {
		t.Fatalf("maintenance: %d %q", code, body)
	}
	if code, _, _ := get("/any/page"); code != http.StatusServiceUnavailable {
		t.Fatalf("inner page: %d", code)
	}
	// What stays up: health, robots, sitemap, theme assets and the admin.
	for _, p := range []string{"/healthz", "/robots.txt", "/sitemap.xml", "/themes/mini/assets/a.css"} {
		if code, _, _ := get(p); code == http.StatusServiceUnavailable {
			t.Errorf("%s is under maintenance", p)
		}
	}
	if code, _, _ := get("/admin/login"); code == http.StatusServiceUnavailable {
		t.Error("the admin is under maintenance")
	}
	app.Deps.Settings.Set(ctx, site.SettingMaintenance, false)
	if code, _, _ := get("/"); code != 200 {
		t.Fatalf("after: %d", code)
	}
}
