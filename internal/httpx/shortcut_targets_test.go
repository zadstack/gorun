package httpx

import (
	"reflect"
	"testing"
)

func TestShortcutTargetsFollowAuthorizedNavigation(t *testing.T) {
	items := []NavItem{
		{ID: "dashboard", Href: "/admin"},
		{ID: "content.type.page", Href: "/admin/content/page"},
		{ID: "content.type.post", Href: "/admin/content/post"},
		{ID: "media", Href: "/admin/media"},
	}
	got := shortcutTargets(items)
	want := []ShortcutTarget{{"h", "/admin"}, {"c", "/admin/content/post"}, {"m", "/admin/media"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	// Without posts the first content type is used; without approvals or settings there is no key.
	got = shortcutTargets([]NavItem{{ID: "content.type.page", Href: "/admin/content/page"}, {ID: "agents.approvals", Href: "/admin/agents/approvals"}})
	want = []ShortcutTarget{{"c", "/admin/content/page"}, {"a", "/admin/agents/approvals"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback targets = %v, want %v", got, want)
	}
	if got := shortcutTargets(nil); len(got) != 0 {
		t.Fatalf("no navigation, targets %v", got)
	}
}
