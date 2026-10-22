package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/media"
)

// Logo variants (dark, light, icon) are ordinary general settings: saved
// from the form and the settings API, validated against existing media,
// and read back; empty values keep the fallback to site.logo.
func TestGeneralSettingsLogoVariants(t *testing.T) {
	s := boot(t, media.New())
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	logo, dark := uploadPNG(t, s), uploadPNG(t, s)
	_, page := s.do("GET", "/admin/settings/general", nil)
	mustContain(t, page, `name="logo_dark"`, `name="logo_light"`, `name="icon"`, "data-logo-variants")
	f := generalForm("https://example.com")
	f.Set("logo", logo)
	f.Set("logo_dark", dark)
	f.Set("icon", "01NOPE")
	if resp, _ := s.post("/admin/settings/general", f); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid icon accepted: %d", resp.StatusCode)
	}
	f.Set("icon", dark)
	if resp, body := s.post("/admin/settings/general", f); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d\n%s", resp.StatusCode, body)
	}
	for key, want := range map[string]string{"site.logo": logo, "site.logo_dark": dark, "site.logo_light": "", "site.icon": dark} {
		if got := s.app.Deps.Settings.GetString(ctx, key, ""); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	admin := s.createUser("api@example.com", "admin", "en")
	code, out := apiCall(t, s, admin, "GET", "/api/v1/settings", "")
	data := out["data"].(map[string]any)
	if code != 200 || data["site.logo_dark"] != dark || data["site.icon"] != dark || data["site.logo_light"] != "" {
		t.Fatalf("GET settings: %d %v", code, data)
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.logo_light":"missing","site.icon":""}`)
	if code != http.StatusUnprocessableEntity || out["error"].(map[string]any)["fields"].(map[string]any)["site.logo_light"] == nil {
		t.Fatalf("bad patch: %d %v", code, out)
	}
	if code, _ := apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.logo_light":"`+logo+`","site.icon":""}`); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.logo_light", ""); got != logo {
		t.Fatalf("site.logo_light = %q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.icon", "x"); got != "" {
		t.Fatalf("site.icon not cleared: %q", got)
	}
}
