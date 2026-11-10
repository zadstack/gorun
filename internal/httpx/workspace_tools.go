package httpx

// WorkspaceTools assigns each standalone shortcut one visible owner. Primary
// navigation, contextual tabs and personal pins take precedence over shortcuts.
type WorkspaceTools struct {
	NavigationURLs             map[string]bool
	Top, Sidebar, Rail, Footer []NavItem
	Groups                     []NavGroup
}

func workspaceTools(groups []NavGroup, sections []NavigationSection, pins []QuickAccessItem) WorkspaceTools {
	var tools WorkspaceTools
	used := map[string]bool{}
	tools.NavigationURLs = used
	for _, section := range sections {
		used[section.Href] = true
		if !section.Active || section.Key == "home" {
			continue
		}
		for _, group := range section.Groups {
			if group.Active || len(section.Groups) == 1 {
				for _, item := range group.Items {
					used[item.Href] = true
				}
			}
		}
	}
	for _, pin := range pins {
		used[pin.Href] = true
	}
	index := map[string]NavItem{}
	for _, group := range groups {
		for _, item := range group.Items {
			index[item.ID] = item
		}
	}
	take := func(ids ...string) []NavItem {
		var out []NavItem
		for _, id := range ids {
			item, ok := index[id]
			if !ok || item.Href == "" || used[item.Href] {
				continue
			}
			used[item.Href] = true
			out = append(out, item)
		}
		return out
	}
	tools.Top = take("media", "site.analytics")
	tools.Sidebar = take("content.type.post", "content.type.page", "site-builder", "menus", "users", "settings.connections")
	tools.Rail = take("appearance.themes", "appearance.templates", "system.backups", "system.health")
	tools.Footer = take("settings")
	for _, group := range groups {
		switch group.Key {
		case "structure", "settings", "integrations", "system":
		default:
			continue
		}
		next := group
		next.Items = nil
		for _, item := range group.Items {
			if item.Href != "" && !used[item.Href] {
				used[item.Href] = true
				next.Items = append(next.Items, item)
			}
		}
		if len(next.Items) > 0 {
			tools.Groups = append(tools.Groups, next)
		}
	}
	return tools
}
