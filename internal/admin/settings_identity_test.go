package admin_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func uploadPNG(t *testing.T, s *site) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	ms, ok := kernel.Get[media.Service](s.app.Deps, kernel.ServiceMedia)
	if !ok {
		t.Fatal("media service missing")
	}
	m, err := ms.Upload(context.Background(), "logo.png", &buf, media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// I4(b): Settings → General writes site.logo (existing media), contact.email
// (valid address) and site.posts_per_page (1–100, stored as a number).
func TestGeneralSettingsLogoContactPerPage(t *testing.T) {
	s := boot(t, media.New())
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	logo := uploadPNG(t, s)

	_, page := s.do("GET", "/admin/settings/general", nil)
	mustContain(t, page, `name="logo"`, `name="contact_email"`, `name="posts_per_page"`, `data-media-open`, "gp-media-picker-body")

	for field, bad := range map[string]string{"logo": "01NOPE", "contact_email": "not-an-email", "posts_per_page": "101"} {
		f := generalForm("https://example.com")
		f.Set(field, bad)
		if resp, _ := s.post("/admin/settings/general", f); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s=%q: %d", field, bad, resp.StatusCode)
		}
	}
	f := generalForm("https://example.com")
	f.Set("logo", logo)
	f.Set("contact_email", "owner@example.com")
	f.Set("posts_per_page", "25")
	if resp, body := s.post("/admin/settings/general", f); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid save: %d\n%s", resp.StatusCode, body)
	}
	var n int
	if err := s.app.Deps.Settings.Get(ctx, "site.posts_per_page", &n); err != nil || n != 25 {
		t.Fatalf("posts_per_page = %d (%v); must be a JSON number", n, err)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.logo", ""); got != logo {
		t.Fatalf("site.logo = %q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "contact.email", ""); got != "owner@example.com" {
		t.Fatalf("contact.email = %q", got)
	}

	admin := s.createUser("api@example.com", "admin", "en")
	code, out := apiCall(t, s, admin, "GET", "/api/v1/settings", "")
	data := out["data"].(map[string]any)
	if code != 200 || data["site.logo"] != logo || data["contact.email"] != "owner@example.com" || data["site.posts_per_page"].(float64) != 25 {
		t.Fatalf("GET settings: %d %v", code, data)
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.posts_per_page":0,"contact.email":"x@","site.logo":"missing"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad patch: %d %v", code, out)
	}
	fields := out["error"].(map[string]any)["fields"].(map[string]any)
	for _, k := range []string{"site.posts_per_page", "contact.email", "site.logo"} {
		if fields[k] == nil {
			t.Fatalf("field %s missing: %v", k, fields)
		}
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.posts_per_page":40,"contact.email":"","site.logo":""}`)
	if code != 200 {
		t.Fatalf("patch: %d %v", code, out)
	}
	if err := s.app.Deps.Settings.Get(ctx, "site.posts_per_page", &n); err != nil || n != 40 {
		t.Fatalf("posts_per_page after PATCH = %d (%v)", n, err)
	}
}
