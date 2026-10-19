package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAgentBundleHTTPWorkflowOwnerCSRFManifestAndDiscoveryState(t *testing.T) {
	s := boot(t)
	owner := s.login("bundle-owner@example.test", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, k, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Writer", owner.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	path := "/admin/agents/keys/" + k.ID + "/bundle"
	id, version := db.NewID(), db.NewID()
	profile := map[string]any{"id": id, "version": version, "kind": "mcp", "name": "Research catalog", "endpoint": "https://mcp.example/PATH_TOKEN_SENTINEL", "secret": "CIPHERTEXT_SENTINEL", "test_status": "pass", "tested_at": "2026-10-19T00:00:00Z", "test_count": 2}
	if e = s.app.Deps.Settings.Set(context.Background(), "connections.profile."+id, profile); e != nil {
		t.Fatal(e)
	}
	form := url.Values{"skill_name": {"Source policy"}, "skill_instructions": {"Keep claims grounded."}, "resource_name": {"Reference"}, "resource_text": {"Never publish automatically."}, "mcp": {id}, "version_" + id: {version}, "reviewed": {"1"}}
	if r, _ := s.do("POST", path, form); r.StatusCode != 403 {
		t.Fatal("missing CSRF", r.StatusCode)
	}
	if r, body := s.post(path, form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode, body)
	}
	_, rendered := s.do("GET", path, nil)
	mustContain(t, rendered, "Bundle saved", "Source policy", "Last tool discovery passed", "2026-10-19T00:00:00Z")
	mustNotContain(t, rendered, secret, "CIPHERTEXT_SENTINEL")
	r, body := s.do("GET", path+"/export", nil)
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatal(r.StatusCode, r.Header)
	}
	mustContain(t, body, "Keep claims grounded.", "Never publish automatically.", "Research catalog", version, `"status":"pass"`)
	mustNotContain(t, body, secret, k.Prefix, "CIPHERTEXT_SENTINEL", "PATH_TOKEN_SENTINEL", "https://", "endpoint", owner.ID, owner.Email, `"scopes"`)
	var manifest map[string]any
	if json.Unmarshal([]byte(body), &manifest) != nil {
		t.Fatal(body)
	}
	if manifest["schema"] != "gorun-agent-bundle-v1" || manifest["agent_id"] != k.ID {
		t.Fatal(manifest)
	}
	var before string
	if e = s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+k.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(before), &stored) != nil {
		t.Fatal(before)
	}
	invalid := url.Values{"version": {stored["version"].(string)}, "reviewed": {"1"}, "skill_name": {"Preserve my proposal"}, "skill_instructions": {strings.Repeat("أ", 2100)}, "resource_name": {""}, "resource_text": {""}}
	if r, body = s.post(path, invalid); r.StatusCode != 422 {
		t.Fatal("UTF-8 byte cap", r.StatusCode)
	} else {
		mustContain(t, body, "Preserve my proposal", strings.Repeat("أ", 2100))
	}
	s.do("GET", "/admin/x/agents-dock", nil)
	var after string
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+k.ID).Scan(&after)
	if before != after {
		t.Fatal("dock GET changed bundle")
	}
	if r, _ = s.post(path, form); r.StatusCode != 409 {
		t.Fatal("stale CAS", r.StatusCode)
	}
	profile["version"] = db.NewID()
	profile["test_status"] = ""
	profile["tested_at"] = ""
	if e = s.app.Deps.Settings.Set(context.Background(), "connections.profile."+id, profile); e != nil {
		t.Fatal(e)
	}
	_, body = s.do("GET", path, nil)
	mustContain(t, body, "Configuration changed")
	mustNotContain(t, body, "Last tool discovery passed", "CIPHERTEXT_SENTINEL")
	if r, _ = s.do("GET", path+"/export", nil); r.StatusCode != 409 {
		t.Fatal("stale export", r.StatusCode)
	}
	s.login("bundle-other@example.test", "admin", "en")
	for _, route := range []string{path, path + "/export"} {
		if r, _ = s.do("GET", route, nil); r.StatusCode != 404 {
			t.Fatal("foreign owner", r.StatusCode)
		}
	}
	if r, _ = s.post(path, form); r.StatusCode != 404 {
		t.Fatal("foreign save", r.StatusCode)
	}
	s.signIn(owner.Email)
	if _, e = s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, owner.ID); e != nil {
		t.Fatal(e)
	}
	for _, route := range []string{path, path + "/export"} {
		r, body = s.do("GET", route, nil)
		if r.StatusCode != 403 && r.StatusCode != 404 {
			t.Fatal("permission loss", r.StatusCode)
		}
		mustNotContain(t, body, "Source policy", "Research catalog")
	}
}
