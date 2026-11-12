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
	for _, group := range tools.Groups {
		for _, item := range group.Items {
			if seen[item.Href] {
				t.Errorf("repeated destination %s (%s)", item.Href, item.ID)
			}
			seen[item.Href] = true
		}
	}
	for _, href := range []string{"/admin/users", "/admin/system/jobs"} {
		if !seen[href] {
			t.Errorf("lost available destination %s", href)
		}
	}
}

// Section destinations belong to their section's tabs: the shell no longer
// repeats them as topbar, sidebar or rail shortcuts, and the dashboard
// directory lists only administration groups.
func TestWorkspaceToolsLeaveSectionDestinationsToTheirTabs(t *testing.T) {
	groups := []NavGroup{
		{Key: "content", Items: []NavItem{{ID: "content.type.page", Href: "/admin/content/page"}, {ID: "media", Href: "/admin/media"}}},
		{Key: "site", Items: []NavItem{{ID: "site.analytics", Href: "/admin/analytics"}}},
		{Key: "appearance", Items: []NavItem{{ID: "appearance.themes", Href: "/admin/appearance/themes"}, {ID: "appearance.templates", Href: "/admin/appearance/templates"}}},
		{Key: "settings", Items: []NavItem{{ID: "settings", Href: "/admin/settings/general"}, {ID: "menus", Href: "/admin/menus"}}},
		{Key: "system", Items: []NavItem{{ID: "system.backups", Href: "/admin/system/backups"}, {ID: "system.health", Href: "/admin/system/health"}}},
	}
	tools := workspaceTools(groups, []NavigationSection{{Href: "/admin/content/page"}, {Href: "/admin/settings/general"}}, nil)
	listed := map[string]bool{}
	for _, group := range tools.Groups {
		for _, item := range group.Items {
			listed[item.Href] = true
		}
	}
	for _, href := range []string{"/admin/media", "/admin/analytics", "/admin/appearance/themes", "/admin/appearance/templates"} {
		if listed[href] || tools.NavigationURLs[href] {
			t.Errorf("%s is owned by its section tabs but was claimed as a shell shortcut", href)
		}
	}
	for _, href := range []string{"/admin/menus", "/admin/system/backups", "/admin/system/health"} {
		if !listed[href] || !tools.NavigationURLs[href] {
			t.Errorf("%s missing from the dashboard directory", href)
		}
	}
}
