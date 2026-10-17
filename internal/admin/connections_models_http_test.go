package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

type modelTransport func(*http.Request) (*http.Response, error)

func (f modelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestConnectionModelsSourceVersionSecretAndNoPersistence(t *testing.T) {
	app, e := kernel.Boot(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, []kernel.Module{settingsmod.New(), auth.New(), New()})
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, e := users.Create(context.Background(), "models@example.com", "Owner", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithUser(context.Background(), u)
	h := &handlers{d: app.Deps}
	secret, e := h.encryptConnectionSecrets(connectionSecrets{Token: "private-stored-key"})
	if e != nil {
		t.Fatal(e)
	}
	c := connectionRecord{ID: db.NewID(), Version: db.NewID(), Name: "Models", Kind: "ai", Provider: "openai", Endpoint: "https://provider.example/v1", Model: "unknown-saved", Secret: secret}
	if e = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	f := url.Values{"id": {c.ID}, "version": {c.Version}, "kind": {"ai"}, "provider": {"openai"}, "endpoint": {c.Endpoint}}
	calls := 0
	mutate := false
	wantToken := "private-stored-key"
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			t.Error("wrong secret source")
		}
		if mutate {
			next := c
			next.Version = db.NewID()
			if e := h.d.Settings.Set(ctx, connectionPrefix+c.ID, next); e != nil {
				t.Error(e)
			}
		}
		w := httptest.NewRecorder()
		fmt.Fprint(w, `{"data":[{"id":"available-model"}]}`)
		return w.Result(), nil
	})}
	run := func(want int) {
		t.Helper()
		req := httptest.NewRequest("POST", "/admin/settings/connections/models", strings.NewReader(f.Encode())).WithContext(ctx)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.connectionsModelsWithClient(w, req, client)
		if w.Code != want {
			t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-stored-key") || strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
	run(200)
	current, _ := h.loadConnection(ctx, c.ID)
	if current != c {
		t.Fatal("discovery mutated saved profile")
	}
	f.Set("version", "stale")
	run(409)
	if calls != 1 {
		t.Fatal("stale request reached provider")
	}
	f.Set("version", c.Version)
	f.Set("endpoint", "https://different.example/v1")
	run(422)
	if calls != 1 {
		t.Fatal("stored credential forwarded to changed endpoint")
	}
	f.Set("token", "explicit-replacement")
	wantToken = "explicit-replacement"
	run(200)
	f.Set("endpoint", c.Endpoint)
	f.Del("token")
	wantToken = "private-stored-key"
	mutate = true
	run(409)
	f.Del("id")
	f.Del("version")
	f.Set("token", "unsaved-key")
	wantToken = "unsaved-key"
	mutate = false
	run(200)
	all, _ := h.listConnections(ctx)
	if len(all) != 1 {
		t.Fatal("unsaved discovery persisted a profile")
	}
}
