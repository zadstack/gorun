package cli

import (
	"bytes"
	"context"
	"github.com/zadstack/gorun/internal/auth"
	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupPreviewGuardIsReadOnlyAndStripsActiveContent(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("forwarded restored/live credentials")
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "gp_session=bad")
		io.WriteString(w, `<html><head><meta http-equiv="refresh" content="0; url=https://live.example"><script>fetch('/danger')</script></head><body><a href="https://live.example/change">live</a><img src="https://live.example/pixel"><p>Snapshot content</p></body></html>`)
	})
	guard := backupPreviewGuard(next, "8080")
	for _, tc := range []struct {
		method, target, peer, host string
		want                       int
	}{
		{"GET", "/", "127.0.0.1:1234", "127.0.0.1:8080", 200},
		{"POST", "/forms/contact", "127.0.0.1:1234", "127.0.0.1:8080", 403},
		{"GET", "/admin", "127.0.0.1:1234", "127.0.0.1:8080", 403},
		{"GET", "/%61pi/v1/users", "127.0.0.1:1234", "127.0.0.1:8080", 403},
		{"GET", "/", "192.168.1.2:1234", "127.0.0.1:8080", 403},
		{"GET", "/", "127.0.0.1:1234", "attacker.example:8080", 403},
	} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.target, nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("Cookie", "gp_session=old")
		r.Header.Set("Authorization", "Bearer old")
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d", tc.method, tc.target, w.Code)
		}
		if w.Code == 200 {
			if w.Header().Get("Set-Cookie") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'none'") {
				t.Fatal("missing preview isolation headers")
			}
			for _, bad := range []string{"live.example", "fetch(", "http-equiv=\"refresh\""} {
				if strings.Contains(w.Body.String(), bad) {
					t.Fatalf("active content left: %s", bad)
				}
			}
			if !strings.Contains(w.Body.String(), "Snapshot content") {
				t.Fatal("preview lost page content")
			}
		}
	}
}

func TestBackupPreviewRejectsPublicListener(t *testing.T) {
	if _, err := prepareBackupPreview(context.Background(), "missing.manifest.json", "0.0.0.0:0"); err == nil {
		t.Fatal("preview accepted public listener")
	}
}

func TestPreparedBackupPreviewUsesDisposableSanitizedCopy(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "site.title", "Snapshot preview fixture"); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "mail.host", "private-mail.example"); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "storage.destination.0123456789abcdef0123456789abcdef", map[string]string{"endpoint": "https://private.example"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "backup.schedule_hours", 24); err != nil {
		t.Fatal(err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	user, err := users.Create(ctx, "owner@example.com", "Owner", "original strong password", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	if _, _, err := keys.Create(ctx, "Live credential", user.ID, []string{"*"}, 60, ""); err != nil {
		t.Fatal(err)
	}
	app.Close()
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "gorun.toml"), []byte("not valid archived configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := gpbackup.CreateWith(cfg.DataDir, cfg.DBPath(), time.Now(), gpbackup.Options{IncludeSecrets: true, SecretKey: cfg.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(res.DB)
	if err != nil {
		t.Fatal(err)
	}
	// These production-like environment values must never reach preview config.
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_SECRET_KEY", "invalid-but-must-not-be-read")
	t.Setenv("STRIPE_SECRET_KEY", "not-a-real-key")
	savedOptions := active
	active.modules = append(append([]ModulesFunc{}, active.modules...), func(config.Config, []kernel.Module) []kernel.Module {
		t.Fatal("preview executed downstream module options")
		return nil
	})
	defer func() { active = savedOptions }()
	preview, err := prepareBackupPreview(ctx, res.Manifest, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sandbox := preview.tempDir
	defer preview.Close()
	if preview.App.Deps.Config.Env != "dev" || bytes.Equal(preview.App.Deps.Config.SecretKey, cfg.SecretKey) || preview.App.Deps.Config.Stripe.SecretKey != "" {
		t.Fatal("preview inherited production configuration")
	}
	for _, name := range []string{"gorun.toml", "secret.key"} {
		if _, err := os.Stat(filepath.Join(preview.DataDir, name)); !os.IsNotExist(err) {
			t.Fatal("archived recovery file left active in preview")
		}
	}
	var n int
	for _, q := range []string{`SELECT count(*) FROM api_keys`, `SELECT count(*) FROM sessions`, `SELECT count(*) FROM magic_links`, `SELECT count(*) FROM jobs`, `SELECT count(*) FROM settings WHERE key LIKE 'mail.%' OR key LIKE 'storage.destination.%'`} {
		if err := preview.App.Deps.DB.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unsanitized preview: %d %v", n, err)
		}
	}
	if _, ok := kernel.Get[any](preview.App.Deps, kernel.ServiceCommerce); ok {
		t.Fatal("commerce code started in preview")
	}
	server := &http.Server{Handler: preview.Handler}
	go server.Serve(preview.Listener)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(preview.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Snapshot preview fixture") || !strings.Contains(string(body), "Backup preview") {
		t.Fatalf("public preview failed: %d", response.StatusCode)
	}
	response, err = client.Post(preview.URL+"/forms/contact", "application/x-www-form-urlencoded", strings.NewReader("message=test"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("preview accepted a form submission")
	}
	after, err := os.ReadFile(res.DB)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("preview modified the original backup")
	}
	preview.Close()
	if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
		t.Fatal("disposable preview directory was not removed")
	}
}

func TestPreviewBlocksExternalRedirectsAndUnsafeURLs(t *testing.T) {
	handler := backupPreviewGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://live.example")
		w.WriteHeader(302)
	}), "8080")
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("Location") != "" {
		t.Fatal("preview followed external redirect")
	}
	for _, raw := range []string{`\\live.example/`, "javascript:alert(1)", "//live.example/", "https://user:pass@127.0.0.1:8080/"} {
		if _, ok := previewURL(raw, "127.0.0.1:8080", false); ok {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	html := previewHTML([]byte(`<html><head><style>.box > p {color: red}</style><meta http-equiv=" refresh " content="0;url=https://live.example"></head><body><p>kept</p></body></html>`), "127.0.0.1:8080")
	if strings.Contains(string(html), "live.example") || !strings.Contains(string(html), ".box > p") {
		t.Fatal("preview refresh not removed or styles damaged")
	}
}
