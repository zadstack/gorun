package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func webmcpJSON(t *testing.T, s *site, method, path, body string, csrf bool) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", s.csrf())
	}
	resp, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestWebMCPToolsActAsTheSignedInUserAndOnlyDraft(t *testing.T) {
	s := boot(t, content.New())
	s.login("webmcp-admin@example.test", "admin", "en")

	// The layout loads the script and its CSRF config on every admin page.
	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `webmcp.js`, `data-webmcp data-csrf=`)

	code, m := webmcpJSON(t, s, "GET", "/admin/x/webmcp/manifest", "", false)
	if code != 200 || m["can_draft"] != true || len(m["pages"].([]any)) == 0 {
		t.Fatalf("manifest %d %v", code, m)
	}
	types := m["content_types"].([]any)
	if len(types) == 0 || types[0].(map[string]any)["slug"] == "" {
		t.Fatalf("manifest content types %v", types)
	}

	code, m = webmcpJSON(t, s, "POST", "/admin/x/webmcp/drafts", `{"type":"post","title":"WebMCP findme draft","body":"<p>Hi</p><script>x()</script>"}`, true)
	if code != 201 || m["status"] != "draft" || !strings.HasPrefix(m["href"].(string), "/admin/content/post/") {
		t.Fatalf("draft %d %v", code, m)
	}
	var status, body string
	if err := s.app.Deps.DB.QueryRow(`SELECT status, body FROM content WHERE id=?`, m["id"]).Scan(&status, &body); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || strings.Contains(body, "<script") {
		t.Fatalf("stored %q %q", status, body)
	}

	code, m = webmcpJSON(t, s, "GET", "/admin/x/webmcp/search?q=findme", "", false)
	if code != 200 || len(m["content"].([]any)) != 1 {
		t.Fatalf("search %d %v", code, m)
	}

	// Without the CSRF token, a cross-site page cannot create drafts.
	if code, _ = webmcpJSON(t, s, "POST", "/admin/x/webmcp/drafts", `{"type":"post","title":"x"}`, false); code != http.StatusForbidden {
		t.Fatal("draft without CSRF", code)
	}
	for _, bad := range []string{`{"type":"post","title":""}`, `{"type":"post","title":"x","status":"published"}`, `{"type":"nope","title":"x"}`, `not json`} {
		if code, m = webmcpJSON(t, s, "POST", "/admin/x/webmcp/drafts", bad, true); code != http.StatusUnprocessableEntity || m["code"] != "validation_failed" {
			t.Fatalf("bad draft %s: %d %v", bad, code, m)
		}
	}
}

func TestWebMCPRespectsPermissions(t *testing.T) {
	s := boot(t, content.New())
	// A role that may only read content: no drafts through WebMCP.
	for _, q := range []string{`INSERT INTO roles (slug, name_json, system) VALUES ('reader', '{"en":"Reader"}', 0)`,
		`INSERT INTO role_permissions (role, permission) VALUES ('reader', 'content.view')`} {
		if _, err := s.app.Deps.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.login("webmcp-reader@example.test", "reader", "en")
	code, m := webmcpJSON(t, s, "GET", "/admin/x/webmcp/manifest", "", false)
	if code == 200 && m["can_draft"] == true {
		t.Fatal("subscriber may draft", m)
	}
	if code, _ = webmcpJSON(t, s, "POST", "/admin/x/webmcp/drafts", `{"type":"post","title":"x"}`, true); code == http.StatusCreated {
		t.Fatal("subscriber created a draft")
	}
	// Signed out, the endpoints are not reachable.
	s.c.Jar = nil
	req, _ := http.NewRequest("GET", s.srv.URL+"/admin/x/webmcp/manifest", nil)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("anonymous manifest")
	}
}
