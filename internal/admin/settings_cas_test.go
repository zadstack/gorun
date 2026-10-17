package admin_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestGeneralSettingsCASPreservesConcurrentFieldsAndInput(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	token := func() string {
		_, page := s.do("GET", "/admin/settings/general", nil)
		m := regexp.MustCompile(`name="edit_token" value="([^"]*)"`).FindStringSubmatch(page)
		if len(m) != 2 || len(m[1]) != 64 {
			t.Fatal("missing native settings token")
		}
		return m[1]
	}
	old := token()
	ctx := context.Background()
	if err := s.app.Deps.Settings.Set(ctx, "site.title", "Concurrent title"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{old, ""} {
		f := generalForm("https://example.com")
		f.Set("edit_token", tok)
		f.Set("tagline", "Unsaved tagline")
		r, page := s.post("/admin/settings/general", f)
		if r.StatusCode != 409 || !strings.Contains(page, "These settings changed elsewhere") || !strings.Contains(page, "Unsaved tagline") || !strings.Contains(page, `name="edit_token" value="`+tok+`"`) {
			t.Fatalf("conflict status=%d", r.StatusCode)
		}
		if s.app.Deps.Settings.GetString(ctx, "site.title", "") != "Concurrent title" || s.app.Deps.Settings.GetString(ctx, "site.tagline", "") != "" {
			t.Fatal("partial or stale write")
		}
	}
	f := generalForm("https://example.com")
	f.Set("edit_token", token())
	f.Set("tagline", "Current tagline")
	if r, _ := s.post("/admin/settings/general", f); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	if s.app.Deps.Settings.GetString(ctx, "site.tagline", "") != "Current tagline" {
		t.Fatal("matching write missing")
	}
	// Raw presence is significant even when the UI displays the same default.
	empty := token()
	if err := s.app.Deps.Settings.Delete(ctx, "site.front_page"); err != nil {
		t.Fatal(err)
	}
	f.Set("edit_token", empty)
	if r, _ := s.post("/admin/settings/general", f); r.StatusCode != 409 {
		t.Fatal("absent/present conflated", r.StatusCode)
	}
}
