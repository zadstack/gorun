package httpx

import "testing"

func TestQuickAccessUsesRegistryAndMostSpecificCurrentItem(t *testing.T) {
	views := quickAccessItems([]NavItem{{ID: "settings", Href: "/admin/settings", LabelKey: "Settings", Group: "settings"}, {ID: "connections", Href: "/admin/settings/connections", LabelKey: "Connections", Group: "integrations"}, {ID: "canvases", Href: "/admin/canvases", Group: "agents"}}, "/admin/settings/connections")
	if len(views) != 3 || views[0].Active || !views[1].Active || views[2].Active || views[2].Category != "canvas" {
		t.Fatal(views)
	}
}
