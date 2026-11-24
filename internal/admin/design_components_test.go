package admin_test

import (
	"strings"
	"testing"
)

// The components page shows the admin's shared components and the site
// components of the core stylesheet, in both languages, behind the settings
// permission.
func TestDesignComponentsPage(t *testing.T) {
	s := boot(t)
	for _, locale := range []string{"en", "ar"} {
		s.login("design-"+locale+"@example.com", "admin", locale)
		response, page := s.do("GET", "/admin/design/components", nil)
		if response.StatusCode != 200 {
			t.Fatalf("%s: status %d", locale, response.StatusCode)
		}
		for _, want := range []string{
			`class="btn" data-variant="outline"`, `class="badge" data-variant="secondary"`,
			`class="alert" data-variant="destructive"`, `aria-invalid="true"`, `role="switch"`,
			`class="dropdown-menu"`, `role="tablist"`, `class="data-table"`, `class="empty-state"`,
			`<div class="gp-core"`, `/static/site/core.css?v=`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: page lacks %s", locale, want)
			}
		}
		if locale == "ar" && !strings.Contains(page, "المكوّنات") {
			t.Error("ar: title not translated")
		}
	}
	s.login("viewer@example.com", "contributor", "en")
	if response, _ := s.do("GET", "/admin/design/components", nil); response.StatusCode != 403 {
		t.Fatalf("contributor: status %d, want 403", response.StatusCode)
	}
}
