package httpx

import (
	"strings"
	"testing"
)

func TestPreferredNavigationGroupsPreserveNewModulesAndItemOrder(t *testing.T) {
	items := []NavItem{{ID: "beta", Group: "system"}, {ID: "alpha", Group: "system"}, {ID: "new-b", Group: "zeta"}, {ID: "home"}, {ID: "new-a", Group: "alpha_module"}, {ID: "content", Group: "content"}}
	groups := groupNav(items, []string{"system", "removed_module", "system", ""})
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.Key)
	}
	if strings.Join(keys, "|") != "system||content|alpha_module|zeta" {
		t.Fatalf("unexpected groups: %q", keys)
	}
	if groups[0].Items[0].ID != "beta" || groups[0].Items[1].ID != "alpha" {
		t.Fatal("grouping changed personal item order")
	}
}
