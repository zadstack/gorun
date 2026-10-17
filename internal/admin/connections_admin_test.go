package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/mailer"
)

func savedConnection(t *testing.T, s *site) map[string]any {
	t.Helper()
	rows, err := s.app.Deps.Settings.All(context.Background(), "connections.profile.")
	if err != nil || len(rows) != 1 {
		t.Fatalf("connections count %d: %v", len(rows), err)
	}
	for _, raw := range rows {
		var out map[string]any
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return nil
}
func TestConnectionsSaveSecretsPreserveClearAndActivate(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	form := url.Values{"name": {"Writing provider"}, "kind": {"ai"}, "endpoint": {"https://api.example.com/v1"}, "provider": {"openai"}, "model": {"my-model"}, "token": {"private-test-token"}}
	if resp, _ := s.post("/admin/settings/connections", form); resp.StatusCode != 303 {
		t.Fatalf("save %d", resp.StatusCode)
	}
	row := savedConnection(t, s)
	secret := row["secret"].(string)
	if strings.Contains(secret, "private-test-token") {
		t.Fatal("plaintext stored")
	}
	raw, err := mailer.DecryptSecret(s.app.Deps.Config.SecretKey, secret)
	if err != nil || !strings.Contains(raw, "private-test-token") {
		t.Fatal("encrypted token not recoverable")
	}
	_, body := s.do("GET", "/admin/settings/connections?edit="+row["id"].(string), nil)
	mustContain(t, body, "Writing provider", "Saved · not tested")
	mustNotContain(t, body, "private-test-token", secret)
	if s.app.Deps.Settings.GetString(context.Background(), "ai.provider", "") != "" {
		t.Fatal("save silently activated AI")
	}
	form.Set("id", row["id"].(string))
	form.Set("version", row["version"].(string))
	form.Set("token", "")
	if resp, _ := s.post("/admin/settings/connections", form); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	next := savedConnection(t, s)
	preserved, _ := mailer.DecryptSecret(s.app.Deps.Config.SecretKey, next["secret"].(string))
	if preserved != raw {
		t.Fatal("blank secret lost stored value")
	}
	if resp, _ := s.post("/admin/settings/connections", form); resp.StatusCode != 409 {
		t.Fatalf("stale save %d", resp.StatusCode)
	}
	action := url.Values{"version": {next["version"].(string)}, "ai_edit_token": {aiFormToken(t, s)}}
	if resp, _ := s.post("/admin/settings/connections/"+row["id"].(string)+"/use-ai", action); resp.StatusCode != 303 {
		t.Fatalf("activate %d", resp.StatusCode)
	}
	if s.app.Deps.Settings.GetString(context.Background(), "ai.model", "") != "my-model" {
		t.Fatal("explicit activation failed")
	}
	form.Set("version", next["version"].(string))
	form.Set("clear_secret", "on")
	if resp, _ := s.post("/admin/settings/connections", form); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	cleared := savedConnection(t, s)
	if value, _ := cleared["secret"].(string); value != "" {
		t.Fatal("secret not cleared")
	}
	var audit string
	if err = s.app.Deps.DB.QueryRow(`SELECT group_concat(meta_json) FROM audit_log WHERE action LIKE 'connections.%'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, "private-test-token") || strings.Contains(audit, secret) {
		t.Fatal("secret in audit")
	}
	if resp, _ := s.post("/admin/settings/connections/"+row["id"].(string)+"/remove", url.Values{"version": {cleared["version"].(string)}}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	rows, err := s.app.Deps.Settings.All(context.Background(), "connections.profile.")
	if err != nil || len(rows) != 0 {
		t.Fatal("removal failed")
	}
	if s.app.Deps.Settings.GetString(context.Background(), "ai.model", "") != "my-model" {
		t.Fatal("profile removal unexpectedly disabled writing")
	}
}
func TestConnectionsPermissionsCSRFAndValidation(t *testing.T) {
	s := boot(t)
	s.login("editor@example.com", "editor", "en")
	for _, route := range []string{"/admin/settings/connections", "/admin/settings/connections/01AAAAAAAAAAAAAAAAAAAAAAAA/test", "/admin/settings/connections/01AAAAAAAAAAAAAAAAAAAAAAAA/remove", "/admin/settings/connections/01AAAAAAAAAAAAAAAAAAAAAAAA/use-ai"} {
		if resp, _ := s.post(route, nil); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("permission %s %d", route, resp.StatusCode)
		}
	}
	if resp, _ := s.do("GET", "/admin/settings/connections", nil); resp.StatusCode != 403 {
		t.Fatal("editor read permitted")
	}
	admin := boot(t)
	admin.login("admin@example.com", "admin", "en")
	form := url.Values{"name": {"Private"}, "kind": {"mcp"}, "endpoint": {"https://127.0.0.1"}, "token": {"do-not-echo"}}
	if resp, _ := admin.do("POST", "/admin/settings/connections", form); resp.StatusCode != 403 {
		t.Fatal("CSRF permitted")
	}
	if resp, body := admin.post("/admin/settings/connections", form); resp.StatusCode != 422 || strings.Contains(body, "do-not-echo") {
		t.Fatal("unsafe endpoint or secret reflected")
	}
	rows, err := admin.app.Deps.Settings.All(context.Background(), "connections.profile.")
	if err != nil || len(rows) > 0 {
		t.Fatal("invalid record persisted")
	}
}

func TestSavedConnectionKindIsReadOnly(t *testing.T) {
	s := boot(t)
	s.login("kind-owner@example.com", "admin", "en")
	form := url.Values{"name": {"MCP fixture"}, "kind": {"mcp"}, "endpoint": {"https://example.com/mcp"}}
	if r, _ := s.post("/admin/settings/connections", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	row := savedConnection(t, s)
	_, body := s.do("GET", "/admin/settings/connections?edit="+row["id"].(string), nil)
	mustContain(t, body, `name="kind" value="mcp"`, `data-saved-kind`, "Create a new connection to use a different type.")
	mustNotContain(t, body, `<select name="kind"`)
}

func TestConnectionStaleSaveRetainsNonSecretProposal(t *testing.T) {
	s := boot(t)
	s.login("proposal@example.com", "admin", "en")
	form := url.Values{"name": {"Original"}, "kind": {"mcp"}, "endpoint": {"https://example.com/mcp"}}
	if r, _ := s.post("/admin/settings/connections", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	row := savedConnection(t, s)
	form.Set("id", row["id"].(string))
	form.Set("version", "old-version")
	form.Set("name", "Unsaved proposal")
	form.Set("token", "must-never-echo")
	r, body := s.post("/admin/settings/connections", form)
	if r.StatusCode != 409 {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, body, `value="Unsaved proposal"`, `value="https://example.com/mcp"`, `value="old-version"`)
	mustNotContain(t, body, "must-never-echo")
}
func TestConnectionActivationRejectsChangedGlobalAI(t *testing.T) {
	s := boot(t)
	s.login("activate-cas@example.com", "admin", "en")
	form := url.Values{"name": {"Writing"}, "kind": {"ai"}, "provider": {"openai"}, "model": {"saved-model"}, "endpoint": {"https://example.com/v1"}}
	if r, _ := s.post("/admin/settings/connections", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	token := aiFormToken(t, s)
	row := savedConnection(t, s)
	if err := s.app.Deps.Settings.Set(context.Background(), "ai.model", "newer-model"); err != nil {
		t.Fatal(err)
	}
	action := url.Values{"version": {row["version"].(string)}, "ai_edit_token": {token}}
	r, _ := s.post("/admin/settings/connections/"+row["id"].(string)+"/use-ai", action)
	if r.StatusCode != 409 {
		t.Fatal("stale activation accepted", r.StatusCode)
	}
}
