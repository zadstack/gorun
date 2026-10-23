package admin_test

import (
	"context"
	"html/template"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
)

func layoutIDs(t *testing.T, s *site) []string {
	t.Helper()
	r, page := s.do("GET", "/admin", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	var ids []string
	for _, m := range regexp.MustCompile(`data-layout-id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids = append(ids, m[1])
	}
	if len(ids) == 0 {
		t.Fatal("missing dashboard layout controls")
	}
	return ids
}

func TestDashboardLayoutOwnershipCASResetAndNewWidgets(t *testing.T) {
	s := boot(t)
	if err := s.app.Deps.Settings.Set(context.Background(), admin.SettingWorkspace, admin.WorkspaceFull); err != nil {
		t.Fatal(err)
	}
	u := s.login("layout@example.com", "admin", "en")
	original := layoutIDs(t, s)
	if original[0] != "setup_status" {
		t.Fatal("setup attention default order", original)
	}
	order := slices.Clone(original)
	slices.Reverse(order)
	in := map[string]any{"version": 0, "order": order}
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", in, false); code != 403 {
		t.Fatal("csrf", code)
	}
	if code, out := studioJSON(t, s, "/admin/dashboard/layout", in, true); code != 200 || out["version"] != float64(1) {
		t.Fatalf("save %d %#v", code, out)
	}
	if !slices.Equal(layoutIDs(t, s), order) {
		t.Fatal("saved order not rendered")
	}
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", in, true); code != 409 {
		t.Fatal("stale", code)
	}
	s.login("other-layout@example.com", "admin", "en")
	if !slices.Equal(layoutIDs(t, s), original) {
		t.Fatal("cross-user order")
	}
	s.signIn(u.Email)
	s.app.Deps.Admin.AddWidget("new_local_widget", func(context.Context, *http.Request) (template.HTML, error) {
		return template.HTML(`<p data-widget="new_local_widget">New module</p>`), nil
	})
	got := layoutIDs(t, s)
	if !slices.Equal(got[:len(order)], order) || got[len(got)-1] != "new_local_widget" {
		t.Fatal("new widget displaced preferences", got)
	}
	for _, bad := range [][]string{append(slices.Clone(got), got[0]), append(slices.Clone(got), "unknown"), got[:len(got)-1]} {
		if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 1, "order": bad}, true); code != 422 {
			t.Fatal("invalid set", code)
		}
	}
	if code, out := studioJSON(t, s, "/admin/dashboard/layout/reset", map[string]any{"version": 1}, true); code != 200 || out["version"] != float64(2) {
		t.Fatalf("reset %d %#v", code, out)
	}
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 1, "order": got}, true); code != 409 {
		t.Fatal("reset replay", code)
	}
	defaults := slices.Clone(original)
	at := slices.Index(defaults, "canvas_gallery")
	if at < 0 {
		at = len(defaults)
	}
	defaults = slices.Insert(defaults, at, "new_local_widget")
	if !slices.Equal(layoutIDs(t, s), defaults) {
		t.Fatal("reset defaults")
	}
}

func TestDashboardLayoutPermissionsAndSetupHonesty(t *testing.T) {
	s := boot(t)
	s.login("setup@example.com", "admin", "en")
	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `data-widget="setup_status"`, `href="/admin/settings/mail"`, "Not configured")
	ctx := context.Background()
	s.app.Deps.Settings.Set(ctx, "ai.provider", "")
	s.app.Deps.Settings.Set(ctx, "mail.host", "smtp.private.example")
	s.app.Deps.Settings.Set(ctx, "mail.password", "hidden-secret-marker")
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, "Intentionally off", "Configured · not checked here")
	mustNotContain(t, page, "smtp.private.example", "hidden-secret-marker")
	s.login("layout-editor@example.com", "editor", "en")
	ids := layoutIDs(t, s)
	_, page = s.do("GET", "/admin", nil)
	mustNotContain(t, page, `data-widget="setup_status"`)
	if slices.Contains(ids, "system_summary") {
		t.Fatal("restricted widget offered")
	}
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 0, "order": append(ids, "system_summary")}, true); code != 422 {
		t.Fatal("restricted widget accepted", code)
	}
	if strings.Contains(page, "data-layout-id=\"setup_status\"") {
		t.Fatal("settings metadata exposed")
	}
}

func TestDashboardSetupResendPresenceWithoutSecrets(t *testing.T) {
	s := boot(t)
	s.login("resend-setup@example.com", "admin", "en")
	ctx := context.Background()
	for k, v := range map[string]string{"mail.provider": "resend", "mail.host": "leftover.smtp.example", "mail.resend_api_key": "encrypted-secret-marker"} {
		if err := s.app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	_, page := s.do("GET", "/admin", nil)
	re := regexp.MustCompile(`(?s)<li[^>]*>.*?/admin/settings/mail.*?</li>`)
	email := re.FindString(page)
	mustContain(t, email, "Not configured")
	mustNotContain(t, page, "encrypted-secret-marker", "leftover.smtp.example")
	if err := s.app.Deps.Settings.Set(ctx, "mail.from", "private-sender@example.com"); err != nil {
		t.Fatal(err)
	}
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, re.FindString(page), "Configured · not checked here")
	mustNotContain(t, page, "private-sender@example.com", "encrypted-secret-marker")
}

func TestDashboardLayoutRetainsPreferencesAcrossRoleChange(t *testing.T) {
	s := boot(t)
	u := s.login("layout-role@example.com", "admin", "en")
	order := layoutIDs(t, s)
	slices.Reverse(order)
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 0, "order": order}, true); code != 200 {
		t.Fatal(code)
	}
	role := "editor"
	if _, err := s.users().Update(context.Background(), u.ID, auth.UserPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	s.signIn(u.Email)
	visible := layoutIDs(t, s)
	if slices.Contains(visible, "setup_status") || slices.Contains(visible, "system_summary") {
		t.Fatal("revoked sections exposed")
	}
	slices.Reverse(visible)
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 1, "order": visible}, true); code != 200 {
		t.Fatal(code)
	}
	role = "admin"
	if _, err := s.users().Update(context.Background(), u.ID, auth.UserPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	s.signIn(u.Email)
	restored := layoutIDs(t, s)
	if !slices.Equal(restored[:len(visible)], visible) || !slices.Contains(restored, "setup_status") {
		t.Fatal("role-aware preferences lost", restored)
	}
	if code, _ := studioJSON(t, s, "/admin/dashboard/layout", map[string]any{"version": 2, "order": restored, "user_id": "another-owner"}, true); code != 422 {
		t.Fatal("unknown field accepted", code)
	}
}
