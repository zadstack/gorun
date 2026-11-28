package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/services"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// newServicesEnv is newEnv with the services module booted before mcp.
func newServicesEnv(t *testing.T) *env {
	t.Helper()
	fake := &fakeModule{}
	app := apitest.Boot(t, content.New(), taxonomy.New(), media.New(), menus.New(),
		theme.New(theme.WithThemesDir("../../themes")), admin.New(), services.New(), fake, New())
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	return &env{t: t, app: app, srv: srv, fake: fake}
}

// set_service parks for a human, and a secret given as a value is refused
// before anything is parked, so it is never stored in mcp_approvals.
func TestSetServiceNeedsApprovalAndNoSecret(t *testing.T) {
	e := newServicesEnv(t)
	ctx := context.Background()
	a := e.agent("settings.manage", "system.capability_test")
	for _, name := range []string{"list_services", "get_service", "set_service", "test_service"} {
		if !contains(a.toolNames(), name) {
			t.Fatalf("%s not listed: %v", name, a.toolNames())
		}
	}
	list := a.ok("list_services", map[string]any{})
	items, _ := list["services"].([]any)
	if len(items) != 6 {
		t.Fatalf("list_services = %v", list)
	}

	chat := map[string]any{"name": "chat", "values": map[string]any{"provider": "chatwoot", "base_url": "https://chat.example.com", "website_token": "tok123abc"}}
	res := a.ok("set_service", chat)
	if str(res, "status") != "pending_approval" {
		t.Fatalf("set_service must park: %v", res)
	}
	if v := e.app.Deps.Settings.GetString(ctx, "chat.provider", ""); v != "" {
		t.Fatalf("saved before approval: %q", v)
	}

	for _, secret := range []string{"password", "api_key"} {
		errObj := a.fails("set_service", map[string]any{"name": "mail", "values": map[string]any{"host": "smtp.example.com", secret: "hunter2-secret"}})
		fields, _ := errObj["fields"].(map[string]any)
		if str(errObj, "code") != "validation_failed" || fields[secret] != "services.error.secret_value" {
			t.Fatalf("%s as a value: %v", secret, errObj)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM mcp_approvals WHERE args_json LIKE '%hunter2%'`); n != 0 {
		t.Fatalf("a secret was parked for approval (%d rows)", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM mcp_approvals WHERE tool = 'set_service'`); n != 1 {
		t.Fatalf("approvals = %d, want 1", n)
	}

	// The approvals page shows what the call would change.
	e.login("admin")
	if _, page := e.do("GET", "/admin/agents/approvals", nil); !strings.Contains(page, "base_url") || !strings.Contains(page, "https://chat.example.com") {
		t.Fatalf("approvals page has no preview of the change:\n%.2000s", page)
	}

	aid := str(res, "approval_id")
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}
	if v := e.app.Deps.Settings.GetString(ctx, "chat.base_url", ""); v != "https://chat.example.com" {
		t.Fatalf("chat.base_url after approval = %q", v)
	}
	var meta string
	if err := e.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'service.update' ORDER BY at DESC LIMIT 1`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, `"via":"mcp"`) || strings.Contains(meta, "tok123abc") {
		t.Fatalf("audit meta = %s", meta)
	}
	got := a.ok("get_service", map[string]any{"name": "chat"})
	if str(got, "state") != services.StateConfigured {
		t.Fatalf("get_service chat = %v", got)
	}
	if errObj := a.fails("get_service", map[string]any{"name": "nope"}); str(errObj, "code") != "not_found" {
		t.Fatalf("unknown service: %v", errObj)
	}
	if tr := a.ok("test_service", map[string]any{"name": "chat"}); tr["ok"] != true {
		t.Fatalf("test_service chat = %v", tr)
	}
}

// run_service takes a backup now, without approval, for a key with
// system.backup; the tool is not listed without that scope.
func TestRunServiceBackups(t *testing.T) {
	e := newServicesEnv(t)
	if a := e.agent("settings.manage"); contains(a.toolNames(), "run_service") {
		t.Fatal("run_service listed without system.backup")
	}
	a := e.agent("settings.manage", "system.backup")
	res := a.ok("run_service", map[string]any{"name": "backups"})
	if res["ok"] != true || res["destination"] != "local" || str(res, "stamp") == "" {
		t.Fatalf("run_service backups = %v", res)
	}
	if errObj := a.fails("run_service", map[string]any{"name": "chat"}); str(errObj, "code") == "" {
		t.Fatalf("run_service chat: %v", errObj)
	}
}

// When the service still lacks its secret, set_service answers with a
// one-time link on the site's base URL, made for the key's owner, and the
// CLI line; nothing in the answer carries a secret.
func TestSetServiceReturnsSecretLink(t *testing.T) {
	e := newServicesEnv(t)
	ctx := context.Background()
	if err := e.app.Deps.Settings.Set(ctx, "site.base_url", "https://site.example"); err != nil {
		t.Fatal(err)
	}
	a := e.agent("settings.manage", "mcp.approve_self")
	data := a.ok("set_service", map[string]any{"name": "mail", "values": map[string]any{
		"provider": "smtp", "host": "smtp.example.com", "port": 465, "tls": "tls", "username": "mailer", "from": "site@example.com"}})
	st, _ := data["status"].(map[string]any)
	if str(st, "state") != services.StateIncomplete || st["secret_set"] != false {
		t.Fatalf("status = %v", data)
	}
	ns, _ := data["needs_secret"].(map[string]any)
	link := str(ns, "link")
	if !strings.HasPrefix(link, "https://site.example/admin/services/mail/secret?t=") || str(ns, "expires_at") == "" {
		t.Fatalf("needs_secret = %v", ns)
	}
	if cli := str(ns, "cli"); cli != `printf '%s\n' "$SECRET" | gorun service set mail --secret-stdin` {
		t.Fatalf("cli = %q", cli)
	}
	if f, _ := st["fields"].(map[string]any); f["host"] != "smtp.example.com" || f["port"] != float64(465) {
		t.Fatalf("fields = %v", st["fields"])
	}

	// The link is stored for the key's owner.
	var raw string
	if err := e.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key LIKE 'services.secret_link.%'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var l struct {
		Name  string `json:"name"`
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil || l.Name != "mail" || l.Actor != a.key.User.ID {
		t.Fatalf("stored link = %s (owner %s)", raw, a.key.User.ID)
	}

	// The link opens the secret page for a signed-in admin.
	e.login("admin")
	if resp, _ := e.do("GET", strings.TrimPrefix(link, "https://site.example"), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("secret page: %d", resp.StatusCode)
	}

	// A service without a secret, or one already complete, gets no link.
	chat := a.ok("set_service", map[string]any{"name": "chat", "values": map[string]any{"provider": "chatwoot", "base_url": "https://chat.example.com", "website_token": "tok12345"}})
	if _, ok := chat["needs_secret"]; ok {
		t.Fatalf("chat needs no secret: %v", chat)
	}
}

// A key that is not one of the service's declared fields is refused before
// the call is parked, so a secret under any other name (secret_key,
// resend_api_key) never lands in mcp_approvals; the error names the key.
func TestSetServiceRefusesUnknownKeyBeforeParking(t *testing.T) {
	e := newServicesEnv(t)
	a := e.agent("settings.manage")
	for _, c := range []struct{ name, key string }{
		{"storage", "secret_key"},
		{"mail", "resend_api_key"},
		{"chat", "api_token"},
	} {
		errObj := a.fails("set_service", map[string]any{"name": c.name, "values": map[string]any{"provider": "r2", c.key: "hunter2-undeclared"}})
		fields, _ := errObj["fields"].(map[string]any)
		if str(errObj, "code") != "validation_failed" || fields[c.key] != "services.error.unknown_field" {
			t.Fatalf("%s %s: %v", c.name, c.key, errObj)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM mcp_approvals WHERE args_json LIKE '%hunter2%'`); n != 0 {
		t.Fatalf("an undeclared key's value was parked for approval (%d rows)", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM mcp_approvals`); n != 0 {
		t.Fatalf("approvals = %d, want 0", n)
	}
}

// tokenInLink matches a raw one-time token in a secret link.
var tokenInLink = regexp.MustCompile(`secret\?t=[A-Za-z0-9_-]{43}`)

// An approved set_service that ends in needs_secret stores its result with
// the link redacted: the raw one-time token never rests in mcp_approvals,
// and check_approval says how to get a fresh link.
func TestSetServiceLinkNotStoredInApprovals(t *testing.T) {
	e := newServicesEnv(t)
	a := e.agent("settings.manage")
	res := a.ok("set_service", map[string]any{"name": "mail", "values": map[string]any{
		"provider": "smtp", "host": "smtp.example.com", "port": 465, "tls": "tls", "username": "mailer", "from": "site@example.com"}})
	aid := str(res, "approval_id")
	if str(res, "status") != "pending_approval" || aid == "" {
		t.Fatalf("set_service must park: %v", res)
	}
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}
	if n := e.count(`SELECT COUNT(*) FROM settings WHERE key LIKE 'services.secret_link.%'`); n != 1 {
		t.Fatalf("links = %d, want 1", n)
	}
	var stored string
	if err := e.app.Deps.DB.QueryRow(`SELECT result_json FROM mcp_approvals WHERE id = ?`, aid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if tokenInLink.MatchString(stored) || !strings.Contains(stored, "redacted") || !strings.Contains(stored, "needs_secret") {
		t.Fatalf("stored result carries the raw link or no redacted one: %s", stored)
	}
	got := a.ok("check_approval", map[string]any{"approval_id": aid})
	raw, _ := json.Marshal(got)
	if tokenInLink.Match(raw) || !strings.Contains(string(raw), "set_service again") {
		t.Fatalf("check_approval = %s", raw)
	}
}
