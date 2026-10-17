package admin_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var plaintextRe = regexp.MustCompile(`gp_[a-z0-9]{40}`)

func TestAPIKeyCreateShowsPlaintextOnce(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, page := s.post("/admin/agents/keys", url.Values{"name": {"agent"}, "scopes": {"content.view", "users.manage"}, "rate": {"30"}, "expires": {"2099-01-31"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d\n%s", resp.StatusCode, page)
	}
	plain := plaintextRe.FindString(page)
	if plain == "" {
		t.Fatalf("plaintext not shown:\n%s", page)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create response must not be cached: %q", resp.Header.Get("Cache-Control"))
	}
	for _, c := range resp.Cookies() {
		if strings.Contains(c.Value, plain[3:]) {
			t.Fatal("plaintext leaked into a cookie")
		}
	}
	mustContain(t, page, `data-key-created`, `hx-history="false"`, `data-label-copied="Copied"`, "content.view", "30/min")
	mustNotContain(t, page, `'Copied'`)

	resp, page = s.do("GET", "/admin/agents/keys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	mustNotContain(t, page, plain, "data-key-created")
	mustContain(t, page, "gp_"+plain[3:11]+"…", "agent")
	if s.auditCount("api_key.create") != 1 {
		t.Fatal("key creation not audited")
	}

	// The screen creates agent keys by default: they authenticate but are
	// refused on the REST API (MCP only).
	mustContain(t, page, `data-key-kind="agent"`, `name="kind" value="agent" checked`)
	apiStatus := func(key string) int {
		req, _ := http.NewRequest("GET", s.srv.URL+"/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		r2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r2.Body.Close()
		return r2.StatusCode
	}
	if st := apiStatus(plain); st != http.StatusForbidden {
		t.Fatalf("agent key on /api: %d, want 403", st)
	}
	resp, page = s.post("/admin/agents/keys", url.Values{"kind": {"api"}, "name": {"ci"}, "all": {"1"}})
	apiPlain := plaintextRe.FindString(page)
	if resp.StatusCode != http.StatusCreated || apiPlain == "" || !strings.Contains(page, `data-key-kind="api"`) {
		t.Fatalf("api key create: %d", resp.StatusCode)
	}
	if st := apiStatus(apiPlain); st == http.StatusUnauthorized || st == http.StatusForbidden {
		t.Fatalf("api key must pass /api authentication: %d", st) // (no api module booted here: 404)
	}

	// Revoke.
	var id string
	s.app.Deps.DB.QueryRow(`SELECT id FROM api_keys WHERE prefix = ?`, plain[3:11]).Scan(&id)
	resp, _ = s.post("/admin/agents/keys/"+id+"/revoke", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	var revoked string
	s.app.Deps.DB.QueryRow(`SELECT revoked_at FROM api_keys WHERE id = ?`, id).Scan(&revoked)
	if revoked == "" || s.auditCount("api_key.revoke", id) != 1 {
		t.Fatal("key not revoked / audited")
	}
}

func TestAPIKeyCreateValidation(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	for name, form := range map[string]url.Values{
		"no scopes":   {"name": {"x"}},
		"no name":     {"scopes": {"content.view"}},
		"bad scope":   {"name": {"x"}, "scopes": {"nope.nope"}},
		"past expiry": {"name": {"x"}, "scopes": {"content.view"}, "expires": {"2000-01-01"}},
		"bad rate":    {"name": {"x"}, "scopes": {"content.view"}, "rate": {"-1"}},
	} {
		resp, page := s.post("/admin/agents/keys", form)
		if resp.StatusCode != http.StatusUnprocessableEntity || plaintextRe.MatchString(page) {
			t.Fatalf("%s: %d", name, resp.StatusCode)
		}
	}
	resp, page := s.post("/admin/agents/keys", url.Values{"name": {"all"}, "all": {"1"}, "scopes": {"content.view"}})
	if resp.StatusCode != http.StatusCreated || !plaintextRe.MatchString(page) {
		t.Fatalf("wildcard key: %d", resp.StatusCode)
	}
	var scopes string
	s.app.Deps.DB.QueryRow(`SELECT scopes_json FROM api_keys WHERE name = 'all'`).Scan(&scopes)
	if scopes != `["*"]` {
		t.Fatalf("wildcard scopes: %s", scopes)
	}
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/agents/keys", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor keys: %d", resp.StatusCode)
	}
}
