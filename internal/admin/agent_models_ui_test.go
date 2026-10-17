package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAgentModelEditorOnlyForActiveOwnedAgents(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, page := s.post("/admin/agents/keys", url.Values{"name": {"Writer"}, "scopes": {"content.view"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	var id string
	if e := s.app.Deps.DB.QueryRow(`SELECT id FROM api_keys WHERE name='Writer'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	mustContain(t, page, "/admin/agents/keys?agent_model="+id)
	resp, page = s.do("GET", "/admin/agents/keys?agent_model="+id, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("edit: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-agent-model-form`, `name="mode"`, `value="managed"`, `value="external"`, `name="model"`, `name="connection_id"`)
	mustNotContain(t, page, `name="token"`)
	resp, _ = s.post("/admin/agents/keys/"+id+"/revoke", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	resp, page = s.do("GET", "/admin/agents/keys", nil)
	if resp.StatusCode != 200 || strings.Contains(page, "/admin/agents/keys?agent_model="+id) {
		t.Fatal("revoked agent remains configurable")
	}
}

func TestAgentModelSaveRequiresSessionCSRF(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, _ := s.post("/admin/agents/keys", url.Values{"name": {"Protected"}, "scopes": {"content.view"}})
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode)
	}
	var id string
	if e := s.app.Deps.DB.QueryRow(`SELECT id FROM api_keys WHERE name='Protected'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	resp, _ = s.do("POST", "/admin/agents/keys/"+id+"/model", url.Values{"mode": {"external"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}
	var n int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM settings WHERE key=?`, "agents.model."+id).Scan(&n); e != nil || n != 0 {
		t.Fatal("unauthorized setting created")
	}
	resp, _ = s.post("/admin/agents/keys/"+id+"/model", url.Values{"mode": {"external"}})
	if resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	if s.auditCount("agent.model.updated", id) != 1 {
		t.Fatal("model update not audited")
	}
}
