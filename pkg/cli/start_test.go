package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestStartBootstrapSurvivesRestart(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	password, err := bootstrapStart(ctx, app)
	if err != nil || len(password) != 43 {
		app.Close()
		t.Fatalf("bootstrap failed: %v", err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := users.CheckPassword(ctx, "admin", password)
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	email := "owner@example.com"
	if _, err := users.Update(ctx, u.ID, auth.UserPatch{Email: &email}); err != nil {
		app.Close()
		t.Fatal(err)
	}
	app.Close()
	app, err = kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if again, err := bootstrapStart(ctx, app); err != nil || again != "" {
		t.Fatalf("restart returned a password: %v", err)
	}
	users, _ = kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	for _, identity := range []string{"admin", email} {
		got, err := users.CheckPassword(ctx, identity, password)
		if err != nil || got.ID != u.ID {
			t.Fatalf("identity %s after restart: %v", identity, err)
		}
	}
	if n, err := users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("users=%d: %v", n, err)
	}
}

func TestStartPreservesExistingSite(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.Boot(context.Background(), cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(context.Background(), "existing@example.com", "Owner", "existing strong password", "admin"); err != nil {
		t.Fatal(err)
	}
	if pw, err := bootstrapStart(context.Background(), app); err != nil || pw != "" {
		t.Fatalf("existing site bootstrap: %v", err)
	}
	if _, err := users.CheckPassword(context.Background(), "admin", "existing strong password"); err == nil {
		t.Fatal("alias must not guess an existing admin")
	}
}

func TestStartGuard(t *testing.T) {
	for _, tc := range []struct {
		name, peer, host, method, target string
		status                           int
		credentials                      bool
	}{
		{"local admin", "127.0.0.1:1234", "localhost:8080", "GET", "/admin", 200, true},
		{"LAN home", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/", 200, false},
		{"LAN head", "192.168.1.20:1234", "192.168.1.10:8080", "HEAD", "/about", 200, false},
		{"LAN admin", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/admin/login", 403, false},
		{"forged localhost", "192.168.1.20:1234", "localhost:8080", "GET", "/admin", 403, false},
		{"rebinding", "127.0.0.1:1234", "attacker.example:8080", "GET", "/admin", 403, false},
		{"wrong port", "127.0.0.1:1234", "localhost:9999", "GET", "/admin", 403, false},
		{"public peer", "203.0.113.1:1234", "192.168.1.10:8080", "GET", "/", 403, false},
		{"checkout callback", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/checkout/success", 403, false},
		{"LAN api", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/api/v1/users", 403, false},
		{"encoded admin", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/%61dmin", 403, false},
		{"dot segments", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/foo/../admin", 403, false},
		{"account", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/account", 403, false},
		{"magic login", "192.168.1.20:1234", "192.168.1.10:8080", "GET", "/auth/magic/token", 403, false},
		{"write", "192.168.1.20:1234", "192.168.1.10:8080", "POST", "/contact", 403, false},
		{"local via LAN host", "127.0.0.1:1234", "192.168.1.10:8080", "GET", "/admin", 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				has := r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != ""
				if has != tc.credentials {
					t.Error("incorrect credential isolation")
				}
				w.WriteHeader(200)
			})
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.target, nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Cookie", "session=secret")
			r.Header.Set("Authorization", "Bearer secret")
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			w := httptest.NewRecorder()
			startGuard(next, "8080", []string{"192.168.1.10"}).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d; want %d", w.Code, tc.status)
			}
		})
	}
}

func TestStartRejectsProd(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "https://example.com")
	t.Setenv("GORUN_SECRET_KEY", strings.Repeat("A", 43))
	if err := start([]string{"--data", t.TempDir()}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "use serve") {
		t.Fatalf("%v", err)
	}
}
