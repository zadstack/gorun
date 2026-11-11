package admin_test

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func paletteHrefs(t *testing.T, m map[string]any, key string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	list, ok := m[key].([]any)
	if !ok {
		t.Fatalf("palette %s missing: %v", key, m)
	}
	for _, v := range list {
		item := v.(map[string]any)
		out[item["href"].(string)] = item
	}
	return out
}

// The palette lists the pages and quick actions the user may use, with
// labels in both admin languages, and the layout carries the palette, the
// shortcut map and only the "g" destinations the user's navigation holds.
func TestPaletteListsPermittedPagesAndActionsInBothLanguages(t *testing.T) {
	s := boot(t, content.New())
	s.login("palette-admin@example.test", "admin", "ar")

	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `command-palette.js`, `shortcuts.js`, `data-gp-palette `, `role="combobox"`, `aria-controls="gp-palette-list"`,
		`data-gp-palette-open`, `data-gp-shortcuts `, `data-gp-shortcut-go="h" href="/admin"`, `data-gp-shortcut-go="c" href="/admin/content/post"`,
		`data-gp-shortcut-go="s" href="/admin/settings/general"`, `لوحة الأوامر`, `اختصارات لوحة المفاتيح`)

	code, m := webmcpJSON(t, s, "GET", "/admin/x/palette", "", false)
	if code != 200 {
		t.Fatalf("palette %d %v", code, m)
	}
	pages := paletteHrefs(t, m, "pages")
	settings, ok := pages["/admin/settings/general"]
	if !ok || settings["label"] != "الإعدادات" || !strings.Contains(strings.Join(anyStrings(settings["alt"]), "|"), "Settings") {
		t.Fatalf("settings page %v", settings)
	}
	actions := paletteHrefs(t, m, "actions")
	for _, href := range []string{"/admin/content/post/new", "/admin/users/new", "/admin/settings/general"} {
		if _, ok := actions[href]; !ok {
			t.Errorf("admin palette lacks action %s: %v", href, actions)
		}
	}
	if post := actions["/admin/content/post/new"]; !strings.Contains(strings.Join(anyStrings(post["alt"]), "|"), "New") {
		t.Errorf("new post action has no English label: %v", post)
	}
}

func TestPaletteOffersOnlyWhatTheRoleAllows(t *testing.T) {
	s := boot(t, content.New())
	s.login("palette-author@example.test", "author", "en")
	code, m := webmcpJSON(t, s, "GET", "/admin/x/palette", "", false)
	if code != 200 {
		t.Fatalf("palette %d %v", code, m)
	}
	actions := paletteHrefs(t, m, "actions")
	if _, ok := actions["/admin/content/post/new"]; !ok {
		t.Errorf("author cannot draft from the palette: %v", actions)
	}
	for _, href := range []string{"/admin/users/new", "/admin/agents/keys", "/admin/settings/general"} {
		if _, ok := actions[href]; ok {
			t.Errorf("author offered %s", href)
		}
	}
	pages := paletteHrefs(t, m, "pages")
	for href := range pages {
		if strings.HasPrefix(href, "/admin/settings") || strings.HasPrefix(href, "/admin/users") {
			t.Errorf("author offered page %s", href)
		}
	}
	_, page := s.do("GET", "/admin", nil)
	if strings.Contains(page, `data-gp-shortcut-go="s"`) || strings.Contains(page, `data-gp-shortcut-go="a"`) {
		t.Fatal("author has shortcuts to settings or approvals")
	}
	mustContain(t, page, `data-gp-shortcut-go="c"`)
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
