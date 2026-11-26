package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// The REST operations reach the same registry as the CLI and MCP: list,
// get, a partial PATCH that refuses a secret value and answers needs_secret,
// DELETE (off) and test; an unknown name is 404.
func TestServicesRESTPatchAndList(t *testing.T) {
	e := bootPage(t)
	ctx := context.Background()
	c := apitest.NewClient(t, e.app, "settings.manage", "system.capability_test")

	status, body := c.Get("/v1/site/services")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 5 {
		t.Fatalf("GET /site/services: %d %v", status, body)
	}
	names := []string{}
	for _, it := range items {
		names = append(names, it.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "mail,storage,chat,analytics,backups" {
		t.Fatalf("names = %v", names)
	}

	status, body = c.Patch("/v1/site/services/mail", map[string]any{"values": map[string]any{"host": "smtp.example.com", "password": "hunter2-secret"}})
	errObj, _ := body["error"].(map[string]any)
	fields, _ := errObj["fields"].(map[string]any)
	if status != http.StatusUnprocessableEntity || errObj["code"] != "validation_failed" || fields["password"] != "services.error.secret_value" {
		t.Fatalf("PATCH with a secret: %d %v", status, body)
	}
	if v := e.app.Deps.Settings.GetString(ctx, "mail.host", ""); v != "" {
		t.Fatalf("a refused PATCH saved mail.host = %q", v)
	}

	status, body = c.Patch("/v1/site/services/mail", map[string]any{"values": map[string]any{
		"provider": "smtp", "host": "smtp.example.com", "port": "2525", "tls": "tls", "username": "mailer", "from": "site@example.com"}})
	data, _ := body["data"].(map[string]any)
	st, _ := data["status"].(map[string]any)
	ns, _ := data["needs_secret"].(map[string]any)
	if status != http.StatusOK || st["state"] != "incomplete" || !strings.HasPrefix(ns["link"].(string), "http://site.test/admin/services/mail/secret?t=") {
		t.Fatalf("PATCH mail: %d %v", status, body)
	}
	if strings.Contains(ns["cli"].(string), "hunter2") || ns["expires_at"] == "" {
		t.Fatalf("needs_secret = %v", ns)
	}

	status, body = c.Patch("/v1/site/services/mail", map[string]any{"values": map[string]any{"port": "not-a-port"}})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH bad port: %d %v", status, body)
	}
	if v := e.app.Deps.Settings.GetString(ctx, "mail.host", ""); v != "smtp.example.com" {
		t.Fatalf("partial PATCH lost mail.host: %q", v)
	}

	status, body = c.Patch("/v1/site/services/chat", map[string]any{"values": map[string]any{"provider": "chatwoot", "base_url": "https://chat.example.com", "website_token": "tok12345"}})
	data, _ = body["data"].(map[string]any)
	if _, has := data["needs_secret"]; status != http.StatusOK || has || data["status"].(map[string]any)["state"] != "configured" {
		t.Fatalf("PATCH chat: %d %v", status, body)
	}
	if status, body = c.Get("/v1/site/services/chat"); status != http.StatusOK || body["data"].(map[string]any)["state"] != "configured" {
		t.Fatalf("GET chat: %d %v", status, body)
	}
	if status, body = c.Post("/v1/site/services/chat/test", nil); status != http.StatusOK || body["data"].(map[string]any)["ok"] != true {
		t.Fatalf("POST chat test: %d %v", status, body)
	}
	if status, _ = c.Delete("/v1/site/services/chat", nil); status != http.StatusOK {
		t.Fatalf("DELETE chat: %d", status)
	}
	if v := e.app.Deps.Settings.GetString(ctx, "chat.provider", ""); v != "" {
		t.Fatalf("chat still on after DELETE: %q", v)
	}
	var n int
	e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'service.update' AND meta_json LIKE '%"via":"api"%'`).Scan(&n)
	if n != 3 {
		t.Fatalf("service.update via api rows = %d, want 3", n)
	}

	// Scopes: a key without settings.manage is refused.
	if status, _ = apitest.NewClient(t, e.app, "content.view").Get("/v1/site/services"); status != http.StatusForbidden {
		t.Fatalf("GET without settings.manage: %d", status)
	}
}

func TestServicesRESTUnknownName(t *testing.T) {
	e := bootPage(t)
	c := apitest.NewClient(t, e.app, "settings.manage", "system.capability_test")
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/site/services/nope", nil},
		{"PATCH", "/v1/site/services/nope", map[string]any{"values": map[string]any{"a": "b"}}},
		{"DELETE", "/v1/site/services/nope", nil},
		{"POST", "/v1/site/services/nope/test", nil},
	} {
		if rec := c.Do(req.method, req.path, req.body, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d %s", req.method, req.path, rec.Code, rec.Body.String())
		}
	}
}
