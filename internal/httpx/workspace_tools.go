package httpx

// WorkspaceTools gives each destination one visible owner. Primary sections
// and their contextual tabs own every destination they hold; personal pins own
// theirs. The shell repeats none of them as topbar, sidebar or rail shortcuts
// (the rail holds agents only), and the command palette covers jumping to any
// page. What remains of the administration groups forms the dashboard
// directory, listed once.
type WorkspaceTools struct {
	// NavigationURLs are the destinations with a visible owner on this page;
	// widgets omit their own link to them.
	NavigationURLs map[string]bool
	// Groups is the dashboard directory of administration tools.
	Groups []NavGroup
}

// directoryGroups are the administration groups the dashboard directory lists.
var directoryGroups = map[string]bool{"structure": true, "settings": true, "integrations": true, "system": true}

func workspaceTools(groups []NavGroup, sections []NavigationSection, pins []QuickAccessItem) WorkspaceTools {
	used := map[string]bool{}
	tools := WorkspaceTools{NavigationURLs: used}
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
	for _, group := range groups {
		if !directoryGroups[group.Key] {
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
