package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanWithRolePerms(t *testing.T) {
	newDB(t) // loads the seeded role → permission mapping
	as := func(role, status string) context.Context {
		return WithUser(context.Background(), &User{ID: "u", Role: role, Status: status})
	}
	cases := []struct {
		ctx  context.Context
		perm string
		want bool
	}{
		{as("editor", "active"), "content.publish", true},
		{as("editor", "active"), "users.manage", false},
		{as("editor", "active"), "redirects.manage", true}, // 0002 seed
		{as("author", "active"), "redirects.manage", false},
		{as("author", "active"), "content.delete", false},
		{as("contributor", "active"), "content.edit", true},
		{as("contributor", "active"), "content.publish", false},
		{as("admin", "active"), "users.manage", true},
		{as("admin", "active"), "anything.declared.later", true}, // admin = *
		{as("customer", "active"), "content.view", false},
		{as("editor", "disabled"), "content.view", false},
		{context.Background(), "content.view", false},
	}
	for i, c := range cases {
		if got := Can(c.ctx, c.perm); got != c.want {
			t.Errorf("case %d %s: got %v", i, c.perm, got)
		}
	}
}

func TestScopeAllows(t *testing.T) {
	if !scopeAllows([]string{"*"}, "x.y") || !scopeAllows([]string{"content.view"}, "content.view") || scopeAllows([]string{"content.view"}, "content.edit") || scopeAllows(nil, "content.view") {
		t.Fatal("scope matching")
	}
}

func TestRequireAnyPermAcceptsEitherPermission(t *testing.T) {
	roleCache.mu.Lock()
	saved := roleCache.perms
	roleCache.perms = map[string]map[string]bool{"texter": {"content.edit_text": true}, "nobody": {}}
	roleCache.mu.Unlock()
	t.Cleanup(func() { roleCache.mu.Lock(); roleCache.perms = saved; roleCache.mu.Unlock() })
	h := RequireAnyPerm("content.edit", "content.edit_text")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for role, want := range map[string]int{"texter": 204, "nobody": 403} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/admin/x", nil).WithContext(WithUser(context.Background(), &User{ID: role, Role: role, Status: StatusActive}))
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", role, rec.Code, want)
		}
	}
}

func TestWithGrantLendsPermissionsToThePrincipalOnly(t *testing.T) {
	roleCache.mu.Lock()
	saved := roleCache.perms
	roleCache.perms = map[string]map[string]bool{"studio": {"content.edit_text": true}}
	roleCache.mu.Unlock()
	t.Cleanup(func() { roleCache.mu.Lock(); roleCache.perms = saved; roleCache.mu.Unlock() })
	base := WithUser(context.Background(), &User{ID: "s", Role: "studio", Status: StatusActive})
	granted := WithGrant(base, "content.edit", "content.publish")
	if !Can(granted, "content.edit") || !Can(granted, "content.publish") || !Can(granted, "content.edit_text") {
		t.Fatal("grant not honoured")
	}
	if Can(base, "content.edit") || Can(granted, "users.manage") {
		t.Fatal("grant leaked to the base context or to other permissions")
	}
	if UserFromContext(granted).ID != "s" || ActorFromContext(granted).ID != "s" {
		t.Fatal("the actor must stay the person")
	}
	if WithGrant(context.Background(), "content.edit") != context.Background() {
		t.Fatal("a context without a principal must be returned unchanged")
	}
}
