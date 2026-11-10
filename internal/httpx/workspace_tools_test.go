package httpx

import "testing"

func TestWorkspaceToolsAssignOneOwnerAndRespectExistingNavigation(t *testing.T) {
	groups := []NavGroup{
		{Key: "content", Items: []NavItem{{ID: "content.type.post", Href: "/admin/content/post"}, {ID: "media", Href: "/admin/media"}}},
		{Key: "settings", Items: []NavItem{{ID: "settings", Href: "/admin/settings/general"}, {ID: "users", Href: "/admin/users"}}},
		{Key: "system", Items: []NavItem{{ID: "users-alias", Href: "/admin/users"}, {ID: "system.health", Href: "/admin/system/health"}, {ID: "system.jobs", Href: "/admin/system/jobs"}}},
	}
	sections := []NavigationSection{{Href: "/admin/content/post"}, {Href: "/admin/settings/general"}}
	pins := []QuickAccessItem{{NavItem: NavItem{ID: "system.health", Href: "/admin/system/health"}}}
	tools := workspaceTools(groups, sections, pins)
	seen := map[string]bool{"/admin/content/post": true, "/admin/settings/general": true, "/admin/system/health": true}
	var actual []NavItem
	for _, slot := range [][]NavItem{tools.Top, tools.Sidebar, tools.Rail, tools.Footer} {
		actual = append(actual, slot...)
	}
	for _, group := range tools.Groups {
		actual = append(actual, group.Items...)
	}
	for _, item := range actual {
		if seen[item.Href] {
			t.Errorf("repeated destination %s (%s)", item.Href, item.ID)
		}
		seen[item.Href] = true
	}
	for _, href := range []string{"/admin/media", "/admin/users", "/admin/system/jobs"} {
		if !seen[href] {
			t.Errorf("lost available destination %s", href)
		}
	}
}
