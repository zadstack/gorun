package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	mcpmodule "github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// bootMCP boots the admin with the mcp module after it (as the real binary
// orders them) and content (for content tools), so the in-process MCP
// probe is registered.
func bootMCP(t *testing.T) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), admin.New(), mcpmodule.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &site{t: t, srv: srv, app: app, c: newClient()}
}

// createKey creates an agent key through the console and returns its id
// and plaintext.
func createKey(t *testing.T, s *site, form url.Values) (id, plain string, page string) {
	t.Helper()
	resp, page := s.post("/admin/agents/keys", form)
	plain = plaintextRe.FindString(page)
	if resp.StatusCode != http.StatusCreated || plain == "" {
		t.Fatalf("create key: %d\n%s", resp.StatusCode, page)
	}
	s.app.Deps.DB.QueryRow(`SELECT id FROM api_keys WHERE prefix = ?`, plain[3:11]).Scan(&id)
	return id, plain, page
}

func TestAgentConnectPanelSnippets(t *testing.T) {
	s := bootMCP(t)
	s.login("admin@example.com", "admin", "en")
	id, plain, page := createKey(t, s, url.Values{"name": {"Writer"}, "scopes": {"content.view"}})

	// No site.base_url yet: the snippets use the request origin.
	origin := s.srv.URL
	mustContain(t, page, `data-agent-connect="new"`, "Connect your agent",
		"claude mcp add --transport http gorun "+origin+"/mcp --header &#34;Authorization: Bearer "+plain+"&#34;",
		`&#34;args&#34;: [`, `&#34;--key&#34;,`, `&#34;`+plain+`&#34;`, `&#34;GORUN_DATA&#34;: &#34;/var/lib/gorun&#34;`, `&#34;GORUN_THEMES&#34;`,
		`&#34;url&#34;: &#34;`+origin+`/mcp&#34;`, `&#34;Authorization&#34;: &#34;Bearer `+plain+`&#34;`,
		`action="/admin/agents/keys/test"`, `name="id" value="`+id+`"`, `name="secret" value="`+plain+`"`,
		`hx-post="/admin/agents/keys/test"`, `hx-select="#connect-new-`+id+`-result"`)
	if n := strings.Count(page, `data-label-copied="Copied"`); n < 4 {
		t.Fatalf("copy buttons = %d, want the key's plus one per snippet", n)
	}

	// The stored site.base_url wins over the request origin.
	if err := s.app.Deps.Settings.Set(context.Background(), "site.base_url", "https://cms.example.test/"); err != nil {
		t.Fatal(err)
	}
	_, plain2, page := createKey(t, s, url.Values{"name": {"Second"}, "scopes": {"content.view"}})
	mustContain(t, page, "gorun https://cms.example.test/mcp --header &#34;Authorization: Bearer "+plain2+"&#34;",
		`&#34;url&#34;: &#34;https://cms.example.test/mcp&#34;`)
	mustNotContain(t, page, origin+"/mcp")

	// The list keeps the panel per key with a placeholder: no secret ever.
	resp, page := s.do("GET", "/admin/agents/keys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if found := plaintextRe.FindString(page); found != "" {
		t.Fatalf("list page shows a secret: %s", found)
	}
	mustNotContain(t, page, plain, plain2, `name="secret"`, `data-agent-connect="new"`)
	mustContain(t, page, `data-key-connect="`+id+`"`, `data-agent-connect="row"`, `data-connect-placeholder`,
		"Replace gp_… with the saved key that starts with gp_"+plain[3:11]+"…",
		"gorun https://cms.example.test/mcp --header &#34;Authorization: Bearer gp_…&#34;",
		`&#34;gp_…&#34;`, `&#34;Authorization&#34;: &#34;Bearer gp_…&#34;`)

	// Arabic: the panel is translated.
	s.login("ar@example.com", "admin", "ar")
	_, page = s.do("GET", "/admin/agents/keys", nil)
	mustContain(t, page, "اربط وكيلك", "اختبار الاتصال")
}

func TestAgentConnectTest(t *testing.T) {
	s := bootMCP(t)
	s.login("admin@example.com", "admin", "en")
	id, plain, _ := createKey(t, s, url.Values{"name": {"Reader"}, "scopes": {"content.view"}})

	// From the list: the stored key record, no secret.
	resp, page := s.post("/admin/agents/keys/test", url.Values{"id": {id}, "scope": {"row"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, `data-connect-result="ok"`, "The key works", "Connected to gorun ", "(MCP ",
		"Key kind: AI agent (MCP)", "<code>content.view</code>", "list_content", `<details class="key-connect" open>`)
	mustNotContain(t, page, plain, `data-key-created`)

	// From the created-key panel: the secret authenticates like a bearer
	// key and the panel (with its snippets) stays on the page.
	resp, page = s.post("/admin/agents/keys/test", url.Values{"id": {id}, "scope": {"new"}, "secret": {plain}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test with secret: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-key-created`, `id="connect-new-`+id+`-result"`, `data-connect-result="ok"`, "Bearer "+plain)
	if n := strings.Count(page, "data-connect-result="); n != 1 {
		t.Fatalf("result rendered %d times, want once (in the created-key panel)", n)
	}

	// A wrong secret for the key is refused like /mcp would (-32001).
	wrong := "gp_" + plain[3:11] + strings.Repeat("0", len(plain)-11)
	_, page = s.post("/admin/agents/keys/test", url.Values{"id": {id}, "scope": {"new"}, "secret": {wrong}})
	mustContain(t, page, `data-connect-result="unauthorized"`, "The secret does not match this key", "<code>-32001</code>")

	// Revoked: the hint names the reason.
	if resp, _ := s.post("/admin/agents/keys/"+id+"/revoke", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	resp, page = s.post("/admin/agents/keys/test", url.Values{"id": {id}, "scope": {"row"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test revoked: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-connect-result="revoked"`, "The connection test failed", "This key was revoked",
		"<code>revoked</code> · JSON-RPC <code>-32001</code>", `data-key-connect="`+id+`"`)
	mustNotContain(t, page, `data-connect-result="ok"`)

	// A key whose scopes reach no tool.
	bare, _, _ := createKey(t, s, url.Values{"name": {"Bare"}, "scopes": {"mcp.approve_self"}})
	_, page = s.post("/admin/agents/keys/test", url.Values{"id": {bare}, "scope": {"row"}})
	mustContain(t, page, `data-connect-result="no_tools"`, "its scopes reach no tools", "JSON-RPC <code>-32003</code>")

	// Unknown key.
	resp, page = s.post("/admin/agents/keys/test", url.Values{"id": {"nope"}, "scope": {"row"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown key: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-connect-result="not_found"`, "This key does not exist")
}

func TestAgentConnectTestPermissionAndCSRF(t *testing.T) {
	s := bootMCP(t)
	owner := s.login("admin@example.com", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	_, k, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Agent", owner.ID, []string{"content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	// Without the CSRF token the POST is refused.
	resp, _ := s.do("POST", "/admin/agents/keys/test", url.Values{"id": {k.ID}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no csrf: %d, want 403", resp.StatusCode)
	}

	s.login("contrib@example.com", "contributor", "en")
	resp, page := s.post("/admin/agents/keys/test", url.Values{"id": {k.ID}, "scope": {"row"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("contributor: %d, want 403", resp.StatusCode)
	}
	mustNotContain(t, page, `data-connect-result`)
}

// Without the mcp module the test explains that MCP is off.
func TestAgentConnectTestWithoutMCP(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	id, _, _ := createKey(t, s, url.Values{"name": {"Agent"}, "scopes": {"content.view"}})
	resp, page := s.post("/admin/agents/keys/test", url.Values{"id": {id}, "scope": {"row"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-connect-result="unavailable"`, "The MCP server is turned off on this site")
}
