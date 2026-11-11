package httpx

import "strings"

// ShortcutTarget is the destination of a "g <key>" keyboard sequence in the
// admin (web/admin/static/shortcuts.js).
type ShortcutTarget struct{ Key, Href string }

// shortcutDestinations maps sequence keys to navigation items. A key is
// offered only when the user's authorized navigation holds the item, so a
// shortcut never leads to a page the user may not open. Content prefers
// posts and falls back to the first content type the user may open.
var shortcutDestinations = []struct{ Key, ID, Prefix string }{
	{"h", "dashboard", ""},
	{"c", "content.type.post", "content.type."},
	{"m", "media", ""},
	{"s", "settings", ""},
	{"a", "agents.approvals", ""},
}

func shortcutTargets(items []NavItem) []ShortcutTarget {
	var out []ShortcutTarget
	for _, d := range shortcutDestinations {
		href := ""
		for _, it := range items {
			if it.Href == "" {
				continue
			}
			if it.ID == d.ID {
				href = it.Href
				break
			}
			if href == "" && d.Prefix != "" && strings.HasPrefix(it.ID, d.Prefix) {
				href = it.Href
			}
		}
		if href != "" {
			out = append(out, ShortcutTarget{Key: d.Key, Href: href})
		}
	}
	return out
}

// Shortcuts lists the "g <key>" destinations over every authorized item,
// before personal sidebar visibility: hiding a sidebar link keeps its
// shortcut.
func (p *navProjection) Shortcuts() []ShortcutTarget {
	if p.t.funcs.Nav == nil && p.t.funcs.AllNav == nil {
		return nil
	}
	p.load()
	return shortcutTargets(p.authorized)
}
