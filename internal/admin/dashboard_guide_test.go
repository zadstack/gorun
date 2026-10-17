package admin_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func guideFragment(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `data-widget="workspace_guide"`)
	if start < 0 {
		t.Fatal("workspace guide missing")
	}
	end := strings.Index(page[start:], "</details>")
	if end < 0 {
		t.Fatal("guide details missing")
	}
	return page[start : start+end]
}
func TestDashboardGuideUsesCurrentAuthorizedNavigation(t *testing.T) {
	s := boot(t, content.New())
	s.login("guide-editor@example.com", "editor", "ar")
	_, page := s.do("GET", "/admin", nil)
	guide := guideFragment(t, page)
	mustContain(t, guide, "دليل مساحة العمل", `href="/admin/content/post"`)
	mustNotContain(t, guide, `href="/admin/users"`, `href="/admin/settings/general"`, `href="/admin/launch-sites"`, `data-guide-area="appearance"`)
	if len(regexp.MustCompile(`data-guide-nav=`).FindAllString(guide, -1)) > 6 {
		t.Fatal("too many guide destinations")
	}
	s.login("guide-owner@example.com", "admin", "en")
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "guide.appearance", Href: "/admin/synthetic-appearance", Group: "appearance", LabelKey: "<script>unsafe()</script>", Permission: "settings.manage"})
	_, page = s.do("GET", "/admin", nil)
	guide = guideFragment(t, page)
	mustContain(t, guide, `data-guide-area="appearance"`, `href="/admin/synthetic-appearance"`, `&lt;script&gt;`)
	mustNotContain(t, guide, `<script>unsafe()`)
}
func TestDashboardGuideOmitsUnavailableModuleAreas(t *testing.T) {
	s := boot(t)
	s.login("guide-minimal@example.com", "admin", "en")
	_, page := s.do("GET", "/admin", nil)
	guide := guideFragment(t, page)
	mustNotContain(t, guide, `data-guide-area="inbox"`, `data-guide-area="customers"`, `data-guide-area="appearance"`)
	mustContain(t, guide, "Workspace guide", "saved", "sidebar")
}
