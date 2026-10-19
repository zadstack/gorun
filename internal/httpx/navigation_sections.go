package httpx

import (
	"slices"
	"strings"
)

// NavigationSection presents the authorized, personalized registry without
// changing IDs, routes, permissions or stored preferences.
type NavigationSection struct {
	Key, LabelKey, Icon, Href string
	Active                    bool
	Groups                    []NavigationTabGroup
}
type NavigationTabGroup struct {
	Key, LabelKey, Href string
	Active              bool
	Items               []NavItem
}

func navigationBucket(it NavItem) (string, string) {
	switch it.ID {
	case "dashboard":
		return "home", "home"
	case "canvases", "workagents.workspace":
		return "canvas", "canvas"
	case "menus", "redirects", "settings.seo":
		return "site", "site"
	case "users", "roles":
		return "settings", "team"
	}
	switch it.Group {
	case "content", "structure":
		return "content", it.Group
	case "appearance":
		return "site", "site"
	case "agents":
		return "canvas", "canvas"
	case "inbox":
		return "inbox", "inbox"
	case "commerce":
		return "commerce", "commerce"
	case "settings", "integrations", "system":
		return "settings", it.Group
	default:
		if it.Group == "" {
			return "settings", "other"
		}
		return "settings", "module:" + it.Group
	}
}
func navPathMatches(p, href string) bool {
	return href != "" && (p == href || (href != "/admin" && strings.HasPrefix(p, strings.TrimSuffix(href, "/")+"/")))
}
func navigationLocation(items []NavItem, p string) (string, string) {
	var best NavItem
	for _, it := range items {
		if navPathMatches(p, it.Href) && len(it.Href) > len(best.Href) {
			best = it
		}
	}
	if best.ID != "" {
		return navigationBucket(best)
	}
	alias := ""
	switch {
	case p == "/admin/navigation":
		return "settings", "settings"
	case strings.HasPrefix(p, "/admin/builder/"):
		// Content editors can open the builder without settings.manage.
		// Any authorized Website entry can anchor its contextual navigation.
		for _, it := range items {
			if section, group := navigationBucket(it); section == "site" {
				return section, group
			}
		}
	case p == "/admin/settings/mail" || p == "/admin/settings/ai":
		alias = "settings"
	}
	for _, it := range items {
		if it.ID == alias && alias != "" {
			return navigationBucket(it)
		}
	}
	return "", ""
}
func navigationSections(groups []NavGroup, authorized []NavItem, p string, preferred []string) []NavigationSection {
	defs := []NavigationSection{
		{Key: "home", LabelKey: "core.nav.section.home", Icon: "layout-dashboard"},
		{Key: "content", LabelKey: "core.nav.section.content", Icon: "files"},
		{Key: "site", LabelKey: "core.nav.section.site", Icon: "panels-top-left"},
		{Key: "canvas", LabelKey: "core.nav.section.canvas", Icon: "bot"},
		{Key: "inbox", LabelKey: "core.nav.section.inbox", Icon: "inbox"},
		{Key: "settings", LabelKey: "core.nav.section.settings", Icon: "settings"},
		{Key: "commerce", LabelKey: "core.nav.section.commerce", Icon: "shopping-bag"},
	}
	activeSection, activeGroup := navigationLocation(authorized, p)
	rank := map[string]int{}
	for i, k := range preferred {
		if _, ok := rank[k]; !ok {
			rank[k] = i
		}
	}
	sectionRank := map[string]int{}
	for _, group := range groups {
		for _, it := range group.Items {
			section, tab := navigationBucket(it)
			for i := range defs {
				if defs[i].Key != section {
					continue
				}
				sec := &defs[i]
				sec.Active = section == activeSection
				if sec.Href == "" {
					sec.Href = it.Href
				}
				if r, ok := rank[it.Group]; ok {
					if old, seen := sectionRank[section]; !seen || r < old {
						sectionRank[section] = r
					}
				}
				j := slices.IndexFunc(sec.Groups, func(g NavigationTabGroup) bool { return g.Key == tab })
				if j < 0 {
					label := "core.nav.group." + tab
					switch tab {
					case "site", "canvas", "inbox", "commerce", "home":
						label = sec.LabelKey
					case "team":
						label = "core.nav.section.team"
					case "other":
						label = "core.nav.section.other"
					default:
						if strings.HasPrefix(tab, "module:") {
							label = "core.nav.group." + strings.TrimPrefix(tab, "module:")
						}
					}
					sec.Groups = append(sec.Groups, NavigationTabGroup{Key: tab, LabelKey: label, Href: it.Href, Active: sec.Active && tab == activeGroup})
					j = len(sec.Groups) - 1
				}
				sec.Groups[j].Items = append(sec.Groups[j].Items, it)
			}
		}
	}
	var out []NavigationSection
	for _, sec := range defs {
		if len(sec.Groups) > 0 {
			out = append(out, sec)
		}
	}
	// Map personal group ranks onto merged sections without rewriting preferences.
	if len(preferred) > 0 {
		slices.SortStableFunc(out, func(a, b NavigationSection) int {
			ra, oka := sectionRank[a.Key]
			rb, okb := sectionRank[b.Key]
			if !oka && !okb {
				return 0
			}
			if !oka {
				return 1
			}
			if !okb {
				return -1
			}
			return ra - rb
		})
	}
	return out
}
