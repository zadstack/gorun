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
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAgentModelProviderFailurePreservesNativeForm(t *testing.T) {
	h, ctx, _, p, c := agentModelFixture(t)
	h.tpl, _ = kernel.Get[*httpx.Templates](h.d, kernel.ServiceAdminTemplates)
	client := &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"private-provider-response"}`)
		return w.Result(), nil
	})}
	f := url.Values{"mode": {"managed"}, "connection_id": {c.ID}, "connection_version": {c.Version}, "model": {"chosen-model"}}
	r := httptest.NewRequest("POST", "/admin/agents/keys/"+p.KeyID+"/model", strings.NewReader(f.Encode())).WithContext(ctx)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", p.KeyID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.agentModelSaveWithClient(w, r, client)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `data-agent-model-form`) || !strings.Contains(w.Body.String(), `value="chosen-model"`) {
		t.Fatalf("provider error discarded form or used gateway status: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "private-provider-response") {
		t.Fatal("upstream response exposed")
	}
	cfg, e := h.loadAgentModel(ctx, p.KeyID)
	if e != nil || cfg.Mode != "external" || cfg.Version != "" {
		t.Fatal("provider failure saved assignment")
	}
}

func TestConnectionModelProviderFailureKeepsNativeJSON(t *testing.T) {
	h, ctx, _, _, c := agentModelFixture(t)
	client := &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"private-provider-response"}`)
		return w.Result(), nil
	})}
	f := url.Values{"id": {c.ID}, "version": {c.Version}, "kind": {"ai"}, "provider": {c.Provider}, "endpoint": {c.Endpoint}}
	r := httptest.NewRequest("POST", "/admin/settings/connections/models", strings.NewReader(f.Encode())).WithContext(ctx)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.connectionsModelsWithClient(w, r, client)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"error":"provider_auth"`) {
		t.Fatalf("gateway response instead of native JSON: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "private-provider-response") {
		t.Fatal("provider response exposed")
	}
}
