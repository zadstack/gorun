package httpx

import (
	"slices"
	"testing"
)

func TestNavigationSectionsKeepDestinationsAndRouteOwnership(t *testing.T) {
	items := []NavItem{
		{ID: "dashboard", Href: "/admin"}, {ID: "canvases", Href: "/admin/canvases"},
		{ID: "posts", Href: "/admin/content/post", Group: "content"}, {ID: "taxonomy", Href: "/admin/taxonomies", Group: "structure"},
		{ID: "menus", Href: "/admin/menus", Group: "structure"}, {ID: "redirects", Href: "/admin/redirects", Group: "structure"},
		{ID: "settings.seo", Href: "/admin/settings/seo", Group: "settings"}, {ID: "site-builder", Href: "/admin/site-builder", Group: "appearance"},
		{ID: "agents.keys", Href: "/admin/agents/keys", Group: "agents"}, {ID: "workagents.workspace", Href: "/agents", Group: "system"},
		{ID: "inbox", Href: "/admin/reports", Group: "inbox"}, {ID: "settings", Href: "/admin/settings/general", Group: "settings"},
		{ID: "users", Href: "/admin/users", Group: "settings"}, {ID: "roles", Href: "/admin/roles", Group: "settings"},
		{ID: "jobs", Href: "/admin/system/jobs", Group: "system"}, {ID: "ext", Href: "/admin/ext", Group: "integrations"},
		{ID: "plugin", Href: "/admin/plugin", Group: "field_kit"}, {ID: "ungrouped", Href: "/admin/extra"},
	}
	for _, tc := range []struct{ path, section, group string }{
		{"/admin", "home", "home"}, {"/admin/website", "site", "site-pages"}, {"/admin/content/post/new", "content", "content"},
		{"/admin/taxonomies/tags", "content", "structure"}, {"/admin/menus/new", "site", "site-pages"},
		{"/admin/settings/seo/audit", "site", "site-search"}, {"/admin/redirects", "site", "site-search"}, {"/admin/builder/42", "site", "site-design"},
		{"/agents", "canvas", "canvas"}, {"/admin/canvases/42", "canvas", "canvas"}, {"/admin/agents/keys/new", "canvas", "canvas"},
		{"/admin/reports/1", "inbox", "inbox"}, {"/admin/users/1", "settings", "team"}, {"/admin/roles", "settings", "team"},
		{"/admin/settings/mail", "settings", "settings"}, {"/admin/settings/ai", "settings", "settings"},
		{"/admin/system/jobs", "settings", "system"}, {"/admin/plugin", "settings", "module:field_kit"}, {"/admin/extra", "settings", "other"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			sections := navigationSections(groupNav(items), items, tc.path, nil)
			if len(sections) != 6 {
				t.Fatalf("expected 6 roots: %d", len(sections))
			}
			counts := map[string]int{}
			active := 0
			activeGroups := 0
			for _, s := range sections {
				for _, g := range s.Groups {
					for _, it := range g.Items {
						counts[it.ID]++
					}
					if g.Active {
						activeGroups++
						if s.Key != tc.section || g.Key != tc.group {
							t.Fatalf("wrong active group %s/%s", s.Key, g.Key)
						}
					}
				}
				if s.Active {
					active++
					if s.Key != tc.section {
						t.Fatal(s.Key)
					}
				}
			}
			if active != 1 || activeGroups != 1 {
				t.Fatalf("active roots/groups %d/%d", active, activeGroups)
			}
			for _, it := range items {
				if counts[it.ID] != 1 {
					t.Fatalf("lost or duplicated %s", it.ID)
				}
			}
		})
	}
}
func TestNavigationSectionsRespectVisibleSubsetAndCommerce(t *testing.T) {
	all := []NavItem{{ID: "dashboard", Href: "/admin"}, {ID: "users", Href: "/admin/users", Group: "settings"}, {ID: "roles", Href: "/admin/roles", Group: "settings"}, {ID: "commerce.products", Href: "/admin/products", Group: "commerce"}}
	visible := slices.Clone(all[:1])
	visible = append(visible, all[2])
	sections := navigationSections(groupNav(visible), all, "/admin/users/1", nil)
	if len(sections) != 2 || sections[1].Key != "settings" || !sections[1].Active {
		t.Fatal(sections)
	}
	if sections[1].Href != "/admin/roles" {
		t.Fatal("hidden page became destination")
	}
	if navigationSections(nil, all, "/admin/users", nil) != nil {
		t.Fatal("empty visibility recreated pages")
	}
	sections = navigationSections(groupNav(all), all, "/admin/products/1", nil)
	if sections[len(sections)-1].Key != "commerce" || !sections[len(sections)-1].Active {
		t.Fatal("optional commerce missing")
	}
	if section, _ := navigationLocation(visible, "/admin/users/1"); section != "" {
		t.Fatal("unauthorized parent inferred")
	}
}
func TestNavigationSectionsPreservePersonalRanksAndItems(t *testing.T) {
	items := []NavItem{{ID: "second", Href: "/admin/b", Group: "system"}, {ID: "first", Href: "/admin/a", Group: "system"}, {ID: "dashboard", Href: "/admin"}, {ID: "custom", Href: "/admin/custom", Group: "fresh_module"}}
	pref := []string{"system", ""}
	sections := navigationSections(groupNav(items, pref), items, "/admin/b", pref)
	if sections[0].Key != "settings" || sections[1].Key != "home" {
		t.Fatal(sections)
	}
	if sections[0].Groups[0].Items[0].ID != "second" || sections[0].Groups[0].Items[1].ID != "first" {
		t.Fatal("item order changed")
	}
	if len(sections[0].Groups) != 2 {
		t.Fatal("new module disappeared")
	}
}

func TestNavigationBuilderUsesAuthorizedWebsiteSubset(t *testing.T) {
	items := []NavItem{{ID: "appearance.themes", Href: "/admin/appearance/themes", Group: "appearance"}}
	sections := navigationSections(groupNav(items), items, "/admin/builder/content-id", nil)
	if len(sections) != 1 || !sections[0].Active || sections[0].Key != "site" || sections[0].Href != "/admin/website" {
		t.Fatal("builder lost authorized Website context", sections)
	}
	if section, _ := navigationLocation(nil, "/admin/builder/content-id"); section != "" {
		t.Fatal("inferred unauthorized Website")
	}
}

func TestWebsiteOverviewDoesNotCreateUnauthorizedSection(t *testing.T) {
	items := []NavItem{{ID: "dashboard", Href: "/admin"}}
	sections := navigationSections(groupNav(items), items, "/admin/website", nil)
	for _, s := range sections {
		if s.Key == "site" || s.Active {
			t.Fatal("website inferred without authorized destination", sections)
		}
	}
}

func TestSubscriptionsOnlySectionUsesActualPurpose(t *testing.T) {
	items := []NavItem{{ID: "saas", Href: "/admin/saas", Group: "commerce"}}
	sections := navigationSections(groupNav(items), items, "/admin/saas", nil)
	if len(sections) != 1 || sections[0].LabelKey != "saas.nav" {
		t.Fatal(sections)
	}
	items = append(items, NavItem{ID: "commerce.products", Href: "/admin/products", Group: "commerce"})
	sections = navigationSections(groupNav(items), items, "/admin/products", nil)
	if sections[0].LabelKey != "core.nav.section.commerce" {
		t.Fatal("store management hidden by subscription label", sections)
	}
}
