package admin_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/pkg/cli"
)

type logoBrowserRT struct {
	mu       sync.Mutex
	payloads []json.RawMessage
}

func (f *logoBrowserRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "logo-browser.invalid" || r.URL.Path != "/v1/images/generations" || r.Method != "POST" {
		return nil, fmt.Errorf("fixture refuses all network: %s", r.URL.Host)
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 40<<10))
	if e != nil {
		return nil, e
	}
	f.mu.Lock()
	f.payloads = append(f.payloads, append(json.RawMessage(nil), raw...))
	f.mu.Unlock()
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for y := 0; y < 1024; y++ {
		for x := 0; x < 1024; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 220, G: 40, B: 80, A: 255})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		return nil, e
	}
	body, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(b.Bytes())}}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

func TestLogoBrowserFixture(t *testing.T) {
	if os.Getenv("E2E_LOGO_FIXTURE") != "1" {
		t.Skip("dedicated local browser fixture only")
	}
	data := os.Getenv("E2E_LOGO_DATA")
	port := os.Getenv("E2E_LOGO_PORT")
	if data == "" || port == "" {
		t.Fatal("missing isolated fixture data/port")
	}
	t.Setenv("GORUN_THEMES", "./themes")
	t.Setenv("GORUN_PATTERNS", "./patterns")
	t.Setenv("GORUN_ADDONS", "{}")
	kernel.LogOutput = os.Stdout
	cfg, e := config.Load([]string{"--data", data, "--base-url", "http://127.0.0.1:" + port})
	if e != nil {
		t.Fatal(e)
	}
	app, e := kernel.BootWithOptions(context.Background(), cfg, cli.Modules(cfg), kernel.BootOptions{StartJobs: false})
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	ctx := context.Background()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	roles, _ := kernel.Get[auth.RoleService](app.Deps, kernel.ServiceRoles)
	if _, e = roles.Create(ctx, "logo_settings", map[string]string{"en": "Settings only", "ar": "الإعدادات فقط"}, []string{"settings.manage"}); e != nil {
		t.Fatal(e)
	}
	for _, role := range []string{"admin", "editor", "logo_settings"} {
		if _, e = users.Create(ctx, role+"@logo-browser.test", role, "logo-browser-password-0001", role); e != nil {
			t.Fatal(e)
		}
	}
	profile, e := admin.LogoBrowserSeed(ctx, app.Deps)
	if e != nil {
		t.Fatal(e)
	}
	for key, value := range map[string]any{"site.title": "Untouched live title", "theme.default.cta_label": "Keep CTA", "theme.default.cta_url": "/keep-cta", "admin.mode": "full"} {
		if e = app.Deps.Settings.Set(ctx, key, value); e != nil {
			t.Fatal(e)
		}
	}
	if e = app.Deps.Settings.Set(ctx, "theme.default.identity_draft", map[string]any{"snapshot": map[string]any{"theme": "default", "values": map[string]string{"identity_accent": "#224466"}, "site": map[string]string{"site.title": "Keep draft title", "site.tagline": "Keep draft tagline"}, "links": map[string]string{"cta_label": "Keep draft CTA", "cta_url": "/draft-cta"}}, "base": map[string]string{"site.title": "Untouched live title"}, "saved_at": "2026-10-23", "author": "fixture"}); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{
		`INSERT INTO content(id,type,slug,title,body,status,locale,created_at,updated_at) VALUES('logo-kept-content','post','logo-kept-content','Kept content','<p>Keep body</p>','published','ar','2026-01-01','2026-01-01')`,
		`INSERT INTO menus(id,slug,name,created_at,updated_at) VALUES('logo-kept-menu','logo-kept-menu','Kept menu','2026-01-01','2026-01-01')`,
		`INSERT INTO menu_items(id,menu_id,label,url) VALUES('logo-kept-item','logo-kept-menu','Keep link','/keep')`,
	} {
		if _, e = app.Deps.DB.ExecContext(ctx, query); e != nil {
			t.Fatal(e)
		}
	}
	fake := &logoBrowserRT{}
	defer admin.LogoBrowserTransport(&http.Client{Transport: fake})()
	mux := http.NewServeMux()
	mux.HandleFunc("/fixture/logo/stats", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"calls": len(fake.payloads), "payloads": fake.payloads, "profile": profile})
	})
	mux.Handle("/", app.Handler)
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	select {
	case <-stop:
	case err := <-done:
		if err != http.ErrServerClosed {
			t.Fatal(err)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(closeCtx)
}
