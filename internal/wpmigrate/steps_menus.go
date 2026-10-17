package wpmigrate

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/menus"
)

// importMenu recreates a menu (matched by slug) with its item tree; items
// pointing to imported posts/terms become content/term items, others keep
// their URL (same-site URLs made relative).
func (s *service) importMenu(ctx context.Context, m *migrationRow, it *itemRow) error {
	var wm WPMenu
	if err := json.Unmarshal(it.Payload, &wm); err != nil {
		return err
	}
	it.Would = map[string]any{"action": "create", "items": len(wm.Items)}
	if m.DryRun {
		return nil
	}
	hosts := s.siteHosts(ctx, m)
	byParent := map[int][]WPMenuItem{}
	for _, mi := range wm.Items {
		byParent[mi.Parent] = append(byParent[mi.Parent], mi)
	}
	var build func(parent, depth int) []menus.Item
	build = func(parent, depth int) []menus.Item {
		list := byParent[parent]
		sort.SliceStable(list, func(i, j int) bool { return list[i].Order < list[j].Order })
		var out []menus.Item
		for i, mi := range list {
			item := menus.Item{Label: mi.Title, Position: i, Kind: "custom", URL: relativeURL(mi.URL, hosts)}
			switch mi.Type {
			case "post_type":
				if id, _, ok := s.mapped(ctx, m, "content", mi.Object, mi.ObjectID); ok {
					item.Kind, item.RefID, item.URL = "content", id, ""
					if item.Label == "" {
						if c, err := s.content.Get(ctx, id); err == nil {
							item.Label = c.Title
						}
					}
				}
			case "taxonomy":
				if id, _, ok := s.mapped(ctx, m, "term", mi.Object, mi.ObjectID); ok {
					item.Kind, item.RefID, item.URL = "term", id, ""
				}
			}
			if item.Label == "" {
				item.Label = firstNonEmpty(item.URL, "—")
			}
			if depth < 8 {
				item.Children = build(mi.ID, depth+1)
			}
			out = append(out, item)
		}
		return out
	}
	items := build(0, 0)
	menu, err := s.targetMenu(ctx, m, it, wm)
	if err != nil {
		return err
	}
	if err := s.menus.SaveItems(ctx, menu.ID, items); err != nil {
		return err
	}
	it.LocalKind, it.LocalID = "menu", menu.ID
	return nil
}

// targetMenu returns the menu to fill: the one this source imported before,
// else a new one. A local menu with the same slug that the migrator did not
// create is never replaced: the import goes to <slug>-imported (logged).
func (s *service) targetMenu(ctx context.Context, m *migrationRow, it *itemRow, wm WPMenu) (*menus.Menu, error) {
	all, err := s.menus.List(ctx)
	if err != nil {
		return nil, err
	}
	if id, _, ok := s.mapped(ctx, m, "menu", "nav_menu", wm.ID); ok {
		for i := range all {
			if all[i].ID == id {
				return &all[i], nil
			}
		}
	}
	taken := map[string]bool{}
	for _, mn := range all {
		taken[mn.Slug] = true
	}
	base := menuSlug(wm)
	slug := base
	for i := 1; taken[slug]; i++ {
		slug = base + "-imported"
		if i > 1 {
			slug += "-" + strconv.Itoa(i)
		}
	}
	if slug != base {
		s.logEntry(ctx, m.ID, "warn", it.ID, "menu_slug_taken", map[string]any{"slug": base, "created": slug})
	}
	return s.menus.Save(ctx, menus.Menu{Slug: slug, Name: firstNonEmpty(wm.Name, wm.Slug), Location: menuLocation(wm.Location)})
}

var asciiSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// menuSlug keeps ASCII WordPress menu slugs; others become menu-<wp id>.
func menuSlug(wm WPMenu) string {
	if s := strings.ToLower(wm.Slug); asciiSlug.MatchString(s) && len(s) <= 100 {
		return s
	}
	return "menu-" + strconv.Itoa(wm.ID)
}

// menuLocation maps WordPress theme locations to GoRun's primary/footer.
func menuLocation(loc string) string {
	l := strings.ToLower(loc)
	switch {
	case strings.Contains(l, "footer"):
		return "footer"
	case l == "":
		return ""
	case strings.Contains(l, "primary"), strings.Contains(l, "main"), strings.Contains(l, "header"), l == "menu_1", l == "menu-1":
		return "primary"
	}
	return ""
}
