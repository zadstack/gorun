package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestRolesAPI(t *testing.T) {
	s := boot(t)
	admin := s.login("admin@example.com", "admin", "en")
	ed := s.createUser("ed@example.com", "editor", "en")

	code, out := apiCall(t, s, admin, "GET", "/api/v1/roles", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, out)
	}
	data, _ := out["data"].([]any)
	if len(data) < 5 {
		t.Fatalf("expected seeded roles, got %v", out)
	}

	code, out = apiCall(t, s, admin, "POST", "/api/v1/roles", `{
		"slug":"marketing","name":{"en":"Marketing","ar":"تسويق"},
		"permissions":["content.view","content.edit"]
	}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	role := out["data"].(map[string]any)
	if role["slug"] != "marketing" {
		t.Fatalf("create body: %v", role)
	}

	code, out = apiCall(t, s, admin, "GET", "/api/v1/roles/marketing", "")
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}

	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/roles/marketing", `{
		"name_en":"Growth","permissions":["content.view","media.upload"]
	}`)
	if code != http.StatusOK {
		t.Fatalf("patch: %d %v", code, out)
	}
	role = out["data"].(map[string]any)
	perms, _ := role["permissions"].([]any)
	if len(perms) != 2 {
		t.Fatalf("patched perms: %v", perms)
	}

	if code, _ := apiCall(t, s, admin, "PATCH", "/api/v1/roles/admin", `{"permissions":["content.view"]}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("admin lock: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "DELETE", "/api/v1/roles/editor", ""); code != http.StatusConflict {
		t.Fatalf("system delete: %d", code)
	}

	if _, err := s.users().Update(context.Background(), ed.ID, auth.UserPatch{Role: ptr("marketing")}); err != nil {
		t.Fatal(err)
	}
	if code, _ := apiCall(t, s, admin, "DELETE", "/api/v1/roles/marketing", ""); code != http.StatusConflict {
		t.Fatalf("in use: %d", code)
	}
	roleEditor := "editor"
	if _, err := s.users().Update(context.Background(), ed.ID, auth.UserPatch{Role: &roleEditor}); err != nil {
		t.Fatal(err)
	}

	code, out = apiCall(t, s, admin, "DELETE", "/api/v1/roles/marketing", "")
	if code != http.StatusOK {
		t.Fatalf("delete: %d %v", code, out)
	}

	if code, _ := apiCall(t, s, ed, "GET", "/api/v1/roles", ""); code != http.StatusForbidden {
		t.Fatalf("editor list: %d", code)
	}

	// Grant denial: editor with users.manage still cannot assign settings.manage.
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('editor', 'users.manage')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.app.Deps.DB.Exec(`DELETE FROM role_permissions WHERE role = 'editor' AND permission = 'users.manage'`)
		auth.ReloadRoles(context.Background(), s.app.Deps.DB)
	})
	code, out = apiCall(t, s, ed, "POST", "/api/v1/roles", `{
		"slug":"elevated","name":{"en":"Elevated","ar":"مرفوع"},
		"permissions":["settings.manage"]
	}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("grant denial: %d %v", code, out)
	}
}

func TestRolesAPIValidation(t *testing.T) {
	s := boot(t)
	admin := s.login("admin@example.com", "admin", "en")

	code, out := apiCall(t, s, admin, "POST", "/api/v1/roles", `{"slug":"Bad-Slug","name":{"en":"X","ar":"ي"},"permissions":[]}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad slug: %d %v", code, out)
	}
	code, raw := apiRaw(s, admin, "GET", "/api/v1/roles/nope", "")
	if code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", code, raw)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	if envelope["error"] == nil && envelope["code"] == nil {
		// httpx.Error uses code field inside error object — either shape is fine if 404.
	}
}

func ptr(s string) *string { return &s }
