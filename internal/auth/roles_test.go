package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRoleServiceCRUD(t *testing.T) {
	d := newDB(t)
	a := testDeps(d).Audit
	svc := NewRoleService(d, a, testPerms())
	ctx := context.Background()

	created, err := svc.Create(ctx, "marketing", map[string]string{"en": "Marketing", "ar": "تسويق"},
		[]string{"content.view", "content.edit", "media.upload"})
	if err != nil {
		t.Fatal(err)
	}
	if created.System || created.Slug != "marketing" || len(created.Permissions) != 3 {
		t.Fatalf("create: %+v", created)
	}
	actions := auditActions(t, d)
	if len(actions) == 0 || actions[len(actions)-1] != "role.create" {
		t.Fatalf("audit: %v", actions)
	}

	got, err := svc.Get(ctx, "marketing")
	if err != nil || got.Name["en"] != "Marketing" || got.Name["ar"] != "تسويق" {
		t.Fatalf("get: %v %+v", err, got)
	}

	perms := []string{"content.view", "content.create"}
	updated, err := svc.Update(ctx, "marketing", RolePatch{
		Name:        map[string]string{"en": "Growth", "ar": "نمو"},
		Permissions: &perms,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name["en"] != "Growth" || len(updated.Permissions) != 2 {
		t.Fatalf("update: %+v", updated)
	}
	ctxUser := WithUser(ctx, &User{ID: "u", Role: "marketing", Status: StatusActive})
	if !Can(ctxUser, "content.view") || Can(ctxUser, "content.edit") {
		t.Fatal("ReloadRoles did not apply new grants")
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list {
		if r.Slug == "marketing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("list missing marketing")
	}

	if err := svc.Delete(ctx, "marketing"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "marketing"); !errors.Is(err, ErrRoleNotFound) {
		t.Fatalf("deleted get: %v", err)
	}
}

func TestRoleServiceProtectsSystemAndAdmin(t *testing.T) {
	d := newDB(t)
	svc := NewRoleService(d, nil, testPerms())
	ctx := context.Background()

	if err := svc.Delete(ctx, "editor"); !errors.Is(err, ErrSystemRole) {
		t.Fatalf("delete system: %v", err)
	}
	_, err := svc.Update(ctx, "admin", RolePatch{Permissions: &[]string{"content.view"}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["permissions"] == "" {
		t.Fatalf("admin lock: %v", err)
	}

	_, err = svc.Create(ctx, "Bad-Slug", map[string]string{"en": "X", "ar": "ي"}, nil)
	if !errors.As(err, &ve) || ve.Fields["slug"] == "" {
		t.Fatalf("slug: %v", err)
	}
	_, err = svc.Create(ctx, "wild", map[string]string{"en": "X", "ar": "ي"}, []string{"*"})
	if !errors.As(err, &ve) || ve.Fields["permissions"] == "" {
		t.Fatalf("wildcard: %v", err)
	}
	_, err = svc.Create(ctx, "nope", map[string]string{"en": "X", "ar": "ي"}, []string{"not.a.real.perm"})
	if !errors.As(err, &ve) || ve.Fields["permissions"] == "" {
		t.Fatalf("unknown perm: %v", err)
	}
}

func TestRoleServiceRejectsInUse(t *testing.T) {
	d := newDB(t)
	roles := NewRoleService(d, nil, testPerms())
	users := testService(d)
	ctx := context.Background()

	if _, err := roles.Create(ctx, "seo", map[string]string{"en": "SEO", "ar": "سيو"}, []string{"content.view"}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create(ctx, "seo@example.com", "SEO", "correct horse battery", "seo"); err != nil {
		t.Fatal(err)
	}
	got, err := roles.Get(ctx, "seo")
	if err != nil || got.UserCount != 1 {
		t.Fatalf("user count: %v %+v", err, got)
	}
	if err := roles.Delete(ctx, "seo"); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("in use: %v", err)
	}
}

func TestRoleServiceNameOnlyUpdateSkipsReloadPath(t *testing.T) {
	d := newDB(t)
	svc := NewRoleService(d, testDeps(d).Audit, testPerms())
	ctx := context.Background()
	if _, err := svc.Create(ctx, "pack", map[string]string{"en": "Pack", "ar": "حزمة"}, []string{"content.view"}); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.Update(ctx, "pack", RolePatch{Name: map[string]string{"en": "Pack Two", "ar": "حزمة ٢"}})
	if err != nil || updated.Name["en"] != "Pack Two" || len(updated.Permissions) != 1 {
		t.Fatalf("name update: %v %+v", err, updated)
	}
}
