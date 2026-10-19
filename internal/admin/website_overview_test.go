package admin_test

import (
	"github.com/zadstack/gorun/internal/kernel"
	"strings"
	"testing"
)

func TestWebsiteOverviewUsesAuthorizedNavigation(t *testing.T) {
	s := boot(t)
	s.login("website-ux@example.com", "contributor", "en")
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "website.allowed", Group: "appearance", LabelKey: "Allowed design", Href: "/admin/synthetic-design", Permission: "content.edit"})
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "website.forbidden", Group: "appearance", LabelKey: "Restricted design", Href: "/admin/synthetic-restricted", Permission: "settings.manage"})
	response, page := s.do("GET", "/admin/website", nil)
	if response.StatusCode != 200 || !strings.Contains(page, `data-website-group="design"`) || !strings.Contains(page, `href="/admin/synthetic-design"`) || strings.Contains(page, `href="/admin/synthetic-restricted"`) {
		t.Fatal("website authorization projection failed", response.StatusCode)
	}
}
