package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
)

func TestUsersCRUDAndRoleChangeAudited(t *testing.T) {
	s := boot(t)
	me := s.login("admin@example.com", "admin", "en")

	resp, page := s.do("GET", "/admin/users", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	mustContain(t, page, "admin@example.com")

	// Invalid create re-renders with field errors.
	resp, page = s.post("/admin/users", url.Values{"name": {"X"}, "email": {"bad"}, "role": {"editor"}, "locale": {"en"}, "password": {"short"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create: %d", resp.StatusCode)
	}
	mustContain(t, page, "field-error")

	resp, _ = s.post("/admin/users", url.Values{"name": {"Ed"}, "email": {"ed@example.com"}, "role": {"editor"}, "locale": {"ar"}, "status": {"active"}, "password": {testPassword}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	ed, err := s.users().GetByEmail(context.Background(), "ed@example.com")
	if err != nil || ed.Role != "editor" || ed.Locale != "ar" {
		t.Fatalf("created user: %+v %v", ed, err)
	}
	if s.auditCount("user.create", ed.ID) != 1 {
		t.Fatal("create not audited")
	}

	// Search and role filter.
	_, page = s.do("GET", "/admin/users?q=ed%40", nil)
	mustContain(t, page, "ed@example.com")
	mustNotContain(t, page, "admin@example.com</")
	_, page = s.do("GET", "/admin/users?role=admin", nil)
	mustContain(t, page, "admin@example.com")
	mustNotContain(t, page, "ed@example.com")

	// Edit: role change is audited with from/to.
	resp, _ = s.do("GET", "/admin/users/"+ed.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit form: %d", resp.StatusCode)
	}
	updatesBefore := s.auditCount("user.update", ed.ID)
	resp, _ = s.post("/admin/users/"+ed.ID, url.Values{"name": {"Ed Two"}, "email": {"ed@example.com"}, "role": {"author"}, "locale": {"en"}, "status": {"active"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	ed, _ = s.users().Get(context.Background(), ed.ID)
	if ed.Role != "author" || ed.Name != "Ed Two" {
		t.Fatalf("updated: %+v", ed)
	}
	if s.auditCount("user.update", ed.ID) != updatesBefore+1 || s.auditCount("user.role_change", ed.ID) != 1 {
		t.Fatal("role change not audited")
	}
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.role_change'`).Scan(&meta)
	if !strings.Contains(meta, `"from":"editor"`) || !strings.Contains(meta, `"to":"author"`) {
		t.Fatalf("role change meta: %s", meta)
	}

	// Cannot delete yourself.
	resp, _ = s.post("/admin/users/"+me.ID+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("self delete: %d", resp.StatusCode)
	}
	if _, err := s.users().Get(context.Background(), me.ID); err != nil {
		t.Fatal("self delete must be refused")
	}

	// Delete another user.
	resp, _ = s.post("/admin/users/"+ed.ID+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := s.users().Get(context.Background(), ed.ID); err != auth.ErrNotFound {
		t.Fatalf("user not deleted: %v", err)
	}
	if s.auditCount("user.delete", ed.ID) != 1 {
		t.Fatal("delete not audited")
	}
}

func TestCannotRemoveLastAdmin(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	other := s.createUser("other@example.com", "admin", "en")
	// Demote the other admin: fine, one admin remains.
	resp, _ := s.post("/admin/users/"+other.ID, url.Values{"name": {"o"}, "email": {"other@example.com"}, "role": {"editor"}, "locale": {"en"}, "status": {"active"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("demote: %d", resp.StatusCode)
	}
	// Sign in as a second admin and try to delete / demote / disable the last one.
	me, _ := s.users().GetByEmail(context.Background(), "admin@example.com")
	s.login("sub@example.com", "editor", "en")
	// editors cannot manage users at all
	if resp, _ := s.do("GET", "/admin/users", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor users list: %d", resp.StatusCode)
	}
	s.signIn("admin@example.com")
	// Self-demotion of the last admin is refused.
	resp, page := s.post("/admin/users/"+me.ID, url.Values{"name": {"a"}, "email": {"admin@example.com"}, "role": {"editor"}, "locale": {"en"}, "status": {"active"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("self demote: %d", resp.StatusCode)
	}
	mustContain(t, page, "field-error")
	if u, _ := s.users().Get(context.Background(), me.ID); u.Role != "admin" {
		t.Fatal("last admin demoted")
	}
}

func TestLastAdminGuardDirect(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	a := s.createUser("a@example.com", "admin", "")
	b := s.createUser("b@example.com", "editor", "")
	actor := auth.WithUser(ctx, b) // a non-admin actor (permission checks are elsewhere)
	if err := adminGuardDelete(t, s, actor, a.ID); err == nil {
		t.Fatal("deleting the last admin must fail")
	}
}

// I5: without SMTP the link is not emailed; the admin sees it once (not
// cacheable, out of the HTMX history cache), and it is never re-shown.
func TestMagicLinkShownOnceWhenSMTPUnset(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ed := s.createUser("ed@example.com", "editor", "")
	resp, page := s.post("/admin/users/"+ed.ID+"/magic-link", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("magic link: %d", resp.StatusCode)
	}
	mustContain(t, page, "http://localhost/auth/magic/", `hx-history="false"`, "Mail is not configured")
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM magic_links WHERE user_id = ?`, ed.ID).Scan(&n)
	if n != 1 || s.auditCount("user.magic_link_shown", ed.ID) != 1 || s.auditCount("user.magic_link_sent", ed.ID) != 0 {
		t.Fatalf("magic link rows %d / audit", n)
	}
	var meta string
	if err := s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'user.magic_link_shown'`).Scan(&meta); err != nil {
		t.Fatalf("audit meta: %v", err)
	}
	if strings.Contains(meta, "auth/magic") || !strings.Contains(meta, "ed@example.com") {
		t.Fatalf("token in audit: %s", meta)
	}
	if _, again := s.do("GET", "/admin/users/"+ed.ID, nil); strings.Contains(again, "/auth/magic/") {
		t.Fatal("the link must be shown only once")
	}
}

// With SMTP configured the link is mailed (here the server is unreachable,
// so the admin gets the failure flash and no link on the page).
func TestMagicLinkMailedWhenSMTPSet(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ed := s.createUser("ed@example.com", "editor", "")
	ctx := context.Background()
	for k, v := range map[string]any{"mail.host": "127.0.0.1", "mail.port": 1, "mail.tls": "none", "mail.from": "noreply@example.com"} {
		if err := s.app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	resp, body := s.post("/admin/users/"+ed.ID+"/magic-link", nil)
	if resp.StatusCode != http.StatusSeeOther || strings.Contains(body, "/auth/magic/") {
		t.Fatalf("magic link with smtp: %d %s", resp.StatusCode, body)
	}
	if s.auditCount("user.magic_link_shown", ed.ID) != 0 {
		t.Fatal("must not show the link when mail is configured")
	}
}

func adminGuardDelete(t *testing.T, s *site, ctx context.Context, id string) error {
	t.Helper()
	return admin.GuardDelete(ctx, s.app.Deps, s.users(), id)
}

func TestDeleteLastAdminRefusedViaHTTP(t *testing.T) {
	s := boot(t)
	only := s.createUser("admin@example.com", "admin", "en")
	// Let editors manage users so a non-admin can attempt the delete.
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('editor', 'users.manage')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // roles are a process-wide cache
		s.app.Deps.DB.Exec(`DELETE FROM role_permissions WHERE role = 'editor' AND permission = 'users.manage'`)
		auth.ReloadRoles(context.Background(), s.app.Deps.DB)
	})
	s.login("ed@example.com", "editor", "en")
	resp, _ := s.post("/admin/users/"+only.ID+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := s.users().Get(context.Background(), only.ID); err != nil {
		t.Fatal("the last admin must not be deleted")
	}
	_, page := s.do("GET", "/admin/users", nil)
	mustContain(t, page, "At least one active administrator must remain.")
	if s.auditCount("user.delete", only.ID) != 0 {
		t.Fatal("refused delete must not be audited as a delete")
	}
}

func TestConcurrentMutualDemotionKeepsAnAdmin(t *testing.T) {
	s := boot(t)
	for i := 0; i < 5; i++ {
		a := s.createUser(fmt.Sprintf("a%d@example.com", i), "admin", "en")
		b := s.createUser(fmt.Sprintf("b%d@example.com", i), "admin", "en")
		var wg sync.WaitGroup
		for _, pair := range [][2]*auth.User{{a, b}, {b, a}} {
			wg.Add(1)
			go func(actor, target *auth.User) {
				defer wg.Done()
				apiRaw(s, actor, "PATCH", "/api/v1/users/"+target.ID, `{"role":"editor"}`)
			}(pair[0], pair[1])
		}
		wg.Wait()
		var n int
		s.app.Deps.DB.QueryRow(`SELECT count(*) FROM users WHERE role = 'admin' AND status = 'active'`).Scan(&n)
		if n < 1 {
			t.Fatalf("round %d: no active admin left", i)
		}
		// Reset: demote/delete the survivors so the next round starts from zero admins.
		s.app.Deps.DB.Exec(`DELETE FROM users WHERE role = 'admin' OR id IN (?, ?)`, a.ID, b.ID)
	}
}
