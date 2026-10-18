package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func (s *site) roles() auth.RoleService {
	s.t.Helper()
	svc, ok := kernel.Get[auth.RoleService](s.app.Deps, kernel.ServiceRoles)
	if !ok {
		s.t.Fatal("roles service missing")
	}
	return svc
}

func TestRolesCRUDUI(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")

	resp, page := s.do("GET", "/admin/roles", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	mustContain(t, page, "admin", "editor", "/admin/roles/new")

	resp, page = s.post("/admin/roles", url.Values{
		"slug": {"marketing"}, "name_en": {"Marketing"}, "name_ar": {"تسويق"},
		"permissions": {"content.view", "content.edit", "media.upload"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d %s", resp.StatusCode, page)
	}
	role, err := s.roles().Get(context.Background(), "marketing")
	if err != nil || len(role.Permissions) != 3 {
		t.Fatalf("created: %+v %v", role, err)
	}
	if s.auditCount("role.create", "marketing") != 1 {
		t.Fatal("create not audited")
	}

	resp, page = s.do("GET", "/admin/roles/marketing", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d", resp.StatusCode)
	}
	mustContain(t, page, "Marketing", "content.view", "name=\"permissions\"")

	// Name-only save (permissions unchanged) does not need extra reauth beyond login.
	resp, _ = s.post("/admin/roles/marketing", url.Values{
		"name_en": {"Growth"}, "name_ar": {"نمو"},
		"permissions": {"content.view", "content.edit", "media.upload"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("name save: %d", resp.StatusCode)
	}

	// Permission change with fresh login succeeds (reauth window from sign-in).
	resp, _ = s.post("/admin/roles/marketing", url.Values{
		"name_en": {"Growth"}, "name_ar": {"نمو"},
		"permissions": {"content.view", "content.create"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("perm save: %d", resp.StatusCode)
	}
	role, _ = s.roles().Get(context.Background(), "marketing")
	if len(role.Permissions) != 2 {
		t.Fatalf("perms: %+v", role.Permissions)
	}
	if s.auditCount("role.update", "marketing") < 1 {
		t.Fatal("update not audited")
	}

	resp, _ = s.post("/admin/roles/marketing/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := s.roles().Get(context.Background(), "marketing"); err != auth.ErrRoleNotFound {
		t.Fatalf("still exists: %v", err)
	}
	if s.auditCount("role.delete", "marketing") != 1 {
		t.Fatal("delete not audited")
	}
}

func TestRolesUIReauthOnPermissionChangeAndDelete(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")

	if _, err := s.roles().Create(context.Background(), "seo", map[string]string{"en": "SEO", "ar": "سيو"}, []string{"content.view"}); err != nil {
		t.Fatal(err)
	}

	stale := db.FormatTime(time.Now().UTC().Add(-11 * time.Minute))
	if _, err := s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = ?`, stale); err != nil {
		t.Fatal(err)
	}

	resp, _ := s.post("/admin/roles/seo", url.Values{
		"name_en": {"SEO"}, "name_ar": {"سيو"},
		"permissions": {"content.view", "content.edit"},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/reauth?next="+url.QueryEscape("/admin/roles/seo") {
		t.Fatalf("perm reauth gate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, _ = s.post("/admin/roles/seo/delete", nil)
	want := "/admin/reauth?next=" + url.QueryEscape("/admin/roles/seo")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
		t.Fatalf("delete reauth gate: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestRolesUIProtectsSystemAdmin(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")

	resp, page := s.do("GET", "/admin/roles/admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin form: %d", resp.StatusCode)
	}
	mustContain(t, page, "Administrator role always has every permission")

	resp, _ = s.post("/admin/roles/editor/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("system delete: %d", resp.StatusCode)
	}
	if _, err := s.roles().Get(context.Background(), "editor"); err != nil {
		t.Fatalf("editor deleted: %v", err)
	}
}

func TestRolesCloneQuery(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, page := s.do("GET", "/admin/roles/new?clone=author", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clone: %d", resp.StatusCode)
	}
	mustContain(t, page, `value="content.view"`, `checked`)
	mustNotContain(t, page, `value="*"`)
}
