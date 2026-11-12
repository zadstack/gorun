package auth

import (
	"context"
	"fmt"
	"testing"
)

// A Permission filter lists only active staff whose role grants it, in SQL,
// so the page limit applies after the filter: customers and disabled users
// sorted first by name do not crowd out the people asked for.
func TestListPermissionFilter(t *testing.T) {
	s, ctx := testService(newDB(t)), context.Background()
	for i := range 5 {
		if _, err := s.Create(ctx, fmt.Sprintf("c%d@example.com", i), fmt.Sprintf("Aaa customer %d", i), "", RoleCustomer); err != nil {
			t.Fatal(err)
		}
	}
	editor := mustUser(t, s, "zed@example.com", "editor")
	contributor := mustUser(t, s, "contrib@example.com", "contributor")
	admin := mustUser(t, s, "admin@example.com", "admin")
	off := mustUser(t, s, "off@example.com", "editor")
	disabled := StatusDisabled
	if _, err := s.Update(ctx, off.ID, UserPatch{Status: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO roles (slug, name_json) VALUES ('viewer', '{"en":"Viewer"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO role_permissions (role, permission) VALUES ('viewer', 'content.view')`); err != nil {
		t.Fatal(err)
	}
	viewer := mustUser(t, s, "viewer@example.com", "viewer")

	page, total, err := s.List(ctx, ListQuery{Permission: "content.create", PerPage: 2, Sort: "name", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 {
		t.Fatalf("total %d, page %d: %+v", total, len(page), page)
	}
	all, _, err := s.List(ctx, ListQuery{Permission: "content.create", PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range all {
		got[u.ID] = true
	}
	for _, u := range []*User{editor, contributor, admin} {
		if !got[u.ID] {
			t.Errorf("%s (%s) missing", u.Email, u.Role)
		}
	}
	for _, u := range []*User{off, viewer} {
		if got[u.ID] {
			t.Errorf("%s (%s, %s) listed", u.Email, u.Role, u.Status)
		}
	}
	found, _, err := s.List(ctx, ListQuery{Permission: "content.create", Search: "zed"})
	if err != nil || len(found) != 1 || found[0].ID != editor.ID {
		t.Fatalf("search within the filter: %+v %v", found, err)
	}
}
