package admin_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// grantEditorKeys lets the editor role manage API keys for one test; roles
// are a process-wide cache, so the grant is undone on cleanup.
func grantEditorKeys(t *testing.T, d *sql.DB) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('editor', 'api_keys.manage')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM role_permissions WHERE role = 'editor' AND permission = 'api_keys.manage'`)
		auth.ReloadRoles(context.Background(), d)
	})
}

func revokedAt(t *testing.T, d *sql.DB, id string) string {
	t.Helper()
	var at sql.NullString
	if err := d.QueryRow(`SELECT revoked_at FROM api_keys WHERE id = ?`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at.String
}

func mintKey(t *testing.T, app *kernel.App, kind string, owner *auth.User) string {
	t.Helper()
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	_, k, err := keys.CreateKind(context.Background(), kind, "target", owner.ID, []string{"content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	return k.ID
}

// Holding api_keys.manage reaches only one's own keys; administrators may
// revoke any key.
func TestKeyRevokeAPIRequiresOwnerOrAdmin(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	grantEditorKeys(t, app.Deps.DB)
	adminC := apitest.NewClient(t, app)
	editor := apitest.New(t, app, apitest.Options{Role: "editor", Scopes: []string{"api_keys.manage", "content.view"}})
	editor2 := apitest.New(t, app, apitest.Options{Role: "editor", Scopes: []string{"api_keys.manage", "content.view"}})

	for _, target := range []string{mintKey(t, app, auth.KeyKindAPI, adminC.User), mintKey(t, app, auth.KeyKindAPI, editor2.User)} {
		res := editor.Do("DELETE", "/v1/keys/"+target, nil, nil)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"not_key_owner"`) {
			t.Fatalf("non-admin revoking another user's key: %d %s", res.Code, res.Body)
		}
		if revokedAt(t, app.Deps.DB, target) != "" {
			t.Fatal("a refused revoke must leave the key active")
		}
	}
	own := mintKey(t, app, auth.KeyKindAPI, editor.User)
	if res := editor.Do("DELETE", "/v1/keys/"+own, nil, nil); res.Code != http.StatusOK || revokedAt(t, app.Deps.DB, own) == "" {
		t.Fatalf("owner revoke: %d %s", res.Code, res.Body)
	}
	other := mintKey(t, app, auth.KeyKindAPI, editor2.User)
	if res := adminC.Do("DELETE", "/v1/keys/"+other, nil, nil); res.Code != http.StatusOK || revokedAt(t, app.Deps.DB, other) == "" {
		t.Fatalf("admin revoke: %d %s", res.Code, res.Body)
	}
	if res := editor.Do("DELETE", "/v1/keys/01JNOTAKEY0000000000000000", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing key: %d %s", res.Code, res.Body)
	}
}

func TestKeyRevokeConsoleRequiresOwnerOrAdmin(t *testing.T) {
	s := boot(t)
	grantEditorKeys(t, s.app.Deps.DB)
	boss := s.createUser("boss@example.com", "admin", "en")
	bossKey := mintKey(t, s.app, auth.KeyKindAgent, boss)

	ed := s.login("ed@example.com", "editor", "en")
	resp, _ := s.post("/admin/agents/keys/"+bossKey+"/revoke", nil)
	if resp.StatusCode != http.StatusSeeOther || revokedAt(t, s.app.Deps.DB, bossKey) != "" {
		t.Fatalf("editor revoking the admin's key: %d", resp.StatusCode)
	}
	if s.auditCount("api_key.revoke", bossKey) != 0 {
		t.Fatal("a refused revoke must not be audited as a revoke")
	}
	_, page := s.do("GET", "/admin/agents/keys", nil)
	mustContain(t, page, "or an administrator can revoke it.")
	own := mintKey(t, s.app, auth.KeyKindAgent, ed)
	if resp, _ := s.post("/admin/agents/keys/"+own+"/revoke", nil); resp.StatusCode != http.StatusSeeOther || revokedAt(t, s.app.Deps.DB, own) == "" {
		t.Fatalf("owner revoke: %d", resp.StatusCode)
	}

	edKey := mintKey(t, s.app, auth.KeyKindAgent, ed)
	s.signIn("boss@example.com")
	if resp, _ := s.post("/admin/agents/keys/"+edKey+"/revoke", nil); resp.StatusCode != http.StatusSeeOther || revokedAt(t, s.app.Deps.DB, edKey) == "" {
		t.Fatalf("admin revoke: %d", resp.StatusCode)
	}
}
