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

func TestAgentRolesCatalogAndDraftDoNotMutateOrExpandAuthority(t *testing.T) {
	s := boot(t)
	owner := s.login("roles-owner@example.test", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, k, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Owned role agent", owner.ID, []string{"content.edit"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	profileID, profileVersion := db.NewID(), db.NewID()
	if e = s.app.Deps.Settings.Set(context.Background(), "connections.profile."+profileID, map[string]any{"id": profileID, "version": profileVersion, "kind": "mcp", "name": "Authorized source catalog", "endpoint": "https://mcp.example/CAPABILITY_PATH_SENTINEL", "secret": "ENCRYPTED_SENTINEL", "test_status": "pass", "tested_at": "2026-10-19T00:00:00Z"}); e != nil {
		t.Fatal(e)
	}
	path := "/admin/agents/keys/" + k.ID + "/bundle"
	if r, body := s.post(path, url.Values{"skill_name": {"Old private skill"}, "skill_instructions": {"PRIVATE_RESOURCE_SENTINEL"}, "resource_name": {""}, "resource_text": {""}, "mcp": {profileID}, "version_" + profileID: {profileVersion}, "reviewed": {"1"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode, body)
	}
	var before string
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+k.ID).Scan(&before)
	var auditBefore int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditBefore)
	var countBefore int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&countBefore)
	r, body := s.do("GET", "/admin/agents/templates?role=researcher", nil)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal(r.StatusCode, body)
	}
	mustContain(t, body, "Researcher", "Bilingual editor", "SEO reviewer", "Support knowledge assistant", "Operations reader", "Campaign planner", "Owned role agent", "Authorized source catalog", "historical")
	mustNotContain(t, body, secret, k.Prefix, "CAPABILITY_PATH_SENTINEL", "ENCRYPTED_SENTINEL", "PRIVATE_RESOURCE_SENTINEL")
	r, body = s.do("GET", path+"?role=researcher", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode, body)
	}
	mustContain(t, body, "Unsaved role configuration", "Source discipline", "Research handoff", "REPLACE", "No connection is preselected")
	mustNotContain(t, body, "PRIVATE_RESOURCE_SENTINEL", `value="`+profileID+`" checked`)
	var after string
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+k.ID).Scan(&after)
	var auditAfter int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditAfter)
	if auditBefore != auditAfter {
		t.Fatal("GET mutated audit")
	}
	if before != after {
		t.Fatal("GET changed saved bundle")
	}
	var stored struct{ Version string }
	json.Unmarshal([]byte(before), &stored)
	form := url.Values{"version": {stored.Version}, "skill_name": {"Source discipline"}, "skill_instructions": {"Use only supplied sources. Return a private draft."}, "resource_name": {""}, "resource_text": {""}, "reviewed": {"1"}}
	if r, _ = s.do("POST", path, form); r.StatusCode != 403 {
		t.Fatal("CSRF missing", r.StatusCode)
	}
	if r, body = s.post(path, form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode, body)
	}
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+k.ID).Scan(&after)
	mustNotContain(t, after, "PRIVATE_RESOURCE_SENTINEL", profileID, `"tools":`)
	if r, _ = s.post(path, form); r.StatusCode != 409 {
		t.Fatal("stale template save", r.StatusCode)
	}
	var countAfter int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&countAfter)
	if countAfter != countBefore {
		t.Fatal("template created a key")
	}
	if r, _ = s.do("GET", path+"?role=not-a-role", nil); r.StatusCode != 404 {
		t.Fatal("unknown role", r.StatusCode)
	}
	other := s.login("roles-other@example.test", "admin", "en")
	_, otherKey, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Other owner key", other.ID, []string{"content.edit"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	_, body = s.do("GET", "/admin/agents/templates?role=researcher", nil)
	mustNotContain(t, body, "Owned role agent", k.ID)
	if r, _ = s.do("GET", path+"?role=researcher", nil); r.StatusCode != 404 {
		t.Fatal("other owner draft", r.StatusCode)
	}
	if r, _ = s.do("GET", "/admin/agents/templates?role=researcher&agent="+k.ID, nil); r.StatusCode != 404 {
		t.Fatal("other owner selection", r.StatusCode)
	}
	keys.Revoke(context.Background(), otherKey.ID)
	_, body = s.do("GET", "/admin/agents/templates?role=researcher", nil)
	mustNotContain(t, body, "Other owner key", otherKey.ID)
	s.login("roles-editor@example.test", "editor", "en")
	r, body = s.do("GET", "/admin/agents/templates", nil)
	if r.StatusCode != 200 {
		t.Fatal("team learning", r.StatusCode)
	}
	mustContain(t, body, "Inspect skills", "require key and settings")
	mustNotContain(t, body, "Authorized source catalog", profileID)
	if r, _ = s.do("GET", "/admin/agents/templates?role=researcher&agent="+k.ID, nil); r.StatusCode != 403 {
		t.Fatal("editor cannot apply", r.StatusCode)
	}
	s.login("roles-simple@example.test", "admin", "en")
	s.app.Deps.Settings.Set(context.Background(), "site.workspace", "simple")
	if r, _ = s.do("GET", "/admin/agents/templates", nil); r.StatusCode != 403 {
		t.Fatal("simple workspace", r.StatusCode)
	}
}
