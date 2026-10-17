package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAgentModelHTTPValidatesAvailabilityAndConnectionCAS(t *testing.T) {
	h, ctx, _, p := canvasOperationFixture(t)
	h.tpl, _ = kernel.Get[*httpx.Templates](h.d, kernel.ServiceAdminTemplates)
	secret, e := h.encryptConnectionSecrets(connectionSecrets{Token: "hidden-provider-key"})
	if e != nil {
		t.Fatal(e)
	}
	conn := connectionRecord{ID: db.NewID(), Version: db.NewID(), Name: "Provider", Kind: "ai", Provider: "openai", Endpoint: "https://provider.example/v1", Model: "one", Secret: secret}
	if e = h.d.Settings.Set(ctx, connectionPrefix+conn.ID, conn); e != nil {
		t.Fatal(e)
	}
	f := url.Values{"mode": {"managed"}, "connection_id": {conn.ID}, "connection_version": {conn.Version}, "model": {"one"}}
	calls := 0
	mutate := false
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != conn.Endpoint+"/models" || r.Header.Get("Authorization") != "Bearer hidden-provider-key" {
			t.Error("wrong discovery source")
		}
		if mutate {
			next := conn
			next.Version = db.NewID()
			if e := h.d.Settings.Set(ctx, connectionPrefix+conn.ID, next); e != nil {
				t.Error(e)
			}
		}
		w := httptest.NewRecorder()
		fmt.Fprint(w, `{"data":[{"id":"one"},{"id":"two"}]}`)
		return w.Result(), nil
	})}
	run := func(ctx context.Context, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/admin/agents/keys/"+p.KeyID+"/model", strings.NewReader(f.Encode())).WithContext(ctx)
		route := chi.NewRouteContext()
		route.URLParams.Add("id", p.KeyID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.agentModelSaveWithClient(w, r, client)
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
		if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "hidden-provider-key") {
			t.Fatal("secret exposed")
		}
	}
	run(ctx, 303)
	cfg, e := h.loadAgentModel(ctx, p.KeyID)
	if e != nil || cfg.Model != "one" || cfg.Mode != "managed" {
		t.Fatal(cfg, e)
	}
	f.Set("version", cfg.Version)
	f.Set("model", "missing")
	run(ctx, 422)
	unchanged, _ := h.loadAgentModel(ctx, p.KeyID)
	if unchanged != cfg {
		t.Fatal("unavailable model saved")
	}
	f.Set("model", "two")
	f.Set("version", "stale")
	before := calls
	run(ctx, 409)
	if calls != before {
		t.Fatal("stale assignment called provider")
	}
	f.Set("version", cfg.Version)
	other, e := h.users.Create(context.Background(), "other@example.invalid", "Other", "long-test-password", "admin")
	if e != nil {
		t.Fatal(e)
	}
	run(auth.WithUser(context.Background(), other), 403)
	if calls != before {
		t.Fatal("foreign owner called provider")
	}
	mutate = true
	run(ctx, 409)
	unchanged, _ = h.loadAgentModel(ctx, p.KeyID)
	if unchanged != cfg {
		t.Fatal("connection change raced assignment")
	}
	f.Set("mode", "external")
	f.Set("connection_id", "")
	f.Set("connection_version", "")
	f.Set("model", "")
	before = calls
	run(ctx, 303)
	cfg, e = h.loadAgentModel(ctx, p.KeyID)
	if e != nil || cfg.Mode != "external" || cfg.Model != "" || calls != before {
		t.Fatal("reset called provider or kept model")
	}
}
