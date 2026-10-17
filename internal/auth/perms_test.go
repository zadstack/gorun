package auth

import (
	"context"
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
