package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

// A role that may manage keys but does not hold "*" must not mint a key
// stronger than itself from the admin form.
func TestAPIKeyFormCannotGrantBeyondRole(t *testing.T) {
	s := boot(t)
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('editor', 'api_keys.manage')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // roles are a process-wide cache
		s.app.Deps.DB.Exec(`DELETE FROM role_permissions WHERE role = 'editor' AND permission = 'api_keys.manage'`)
		auth.ReloadRoles(context.Background(), s.app.Deps.DB)
	})
	s.login("ed@example.com", "editor", "en")
	for _, form := range []url.Values{
		{"kind": {"api"}, "name": {"all"}, "all": {"1"}},
		{"kind": {"api"}, "name": {"users"}, "scopes": {"content.view", "users.manage"}},
	} {
		resp, page := s.post("/admin/agents/keys", form)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("granting %v as an editor: %d", form, resp.StatusCode)
		}
		mustContain(t, page, "You can only grant permissions you hold yourself")
		if plaintextRe.MatchString(page) {
			t.Fatal("no key may be issued")
		}
	}
	resp, page := s.post("/admin/agents/keys", url.Values{"kind": {"api"}, "name": {"ok"}, "scopes": {"content.view"}})
	if resp.StatusCode != http.StatusCreated || !plaintextRe.MatchString(page) {
		t.Fatalf("granting a held scope: %d", resp.StatusCode)
	}
}
