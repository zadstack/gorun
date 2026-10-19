package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func quickPayload(t *testing.T, s *site, path string) map[string]any {
	t.Helper()
	r, body := s.do("GET", path, nil)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal(r.StatusCode, body)
	}
	var data map[string]any
	if json.Unmarshal([]byte(body), &data) != nil {
		t.Fatal(body)
	}
	return data
}
func TestSidebarQuickAccessOwnerPermissionsAndReadOnlyProjection(t *testing.T) {
	s := boot(t)
	u := s.login("quick-owner@example.test", "admin", "en")
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}, 200)
	var before int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before)
	data := quickPayload(t, s, "/admin/dashboard/pins?page=/admin/users&widget=1")
	html := data["quick_html"].(string)
	mustContain(t, html, `data-quick-id="users"`, `aria-current="page"`, `href="/admin/users"`)
	mustContain(t, data["widget_html"].(string), `data-pin-id="users"`)
	_, page := s.do("GET", "/admin/canvases", nil)
	mustContain(t, page, `data-sidebar-quick-access`, `data-quick-id="users"`)
	var after int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after)
	if before != after {
		t.Fatal("GET changed audit")
	}
	s.login("quick-other@example.test", "admin", "ar")
	other := quickPayload(t, s, "/admin/dashboard/pins")
	mustNotContain(t, other["quick_html"].(string), `data-quick-id="users"`)
	pinPost(t, s, "/admin/dashboard/pins/users/remove", nil, 200)
	s.signIn(u.Email)
	data = quickPayload(t, s, "/admin/dashboard/pins")
	mustContain(t, data["quick_html"].(string), `data-quick-id="users"`)
	// Existing database roles/current request authorization retire the shortcut,
	// without removing its stored personal placement.
	if _, e := s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, u.ID); e != nil {
		t.Fatal(e)
	}
	data = quickPayload(t, s, "/admin/dashboard/pins")
	mustNotContain(t, data["quick_html"].(string), `href="/admin/users"`)
	var count int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count)
	if count != 1 {
		t.Fatal("read rewrote pins")
	}
	for _, page := range []string{"https://evil.example/admin", "/outside", "/admin?token=secret", "/admin#fragment"} {
		if r, _ := s.do("GET", "/admin/dashboard/pins?page="+url.QueryEscape(page), nil); r.StatusCode != 422 {
			t.Fatal("invalid active path", page, r.StatusCode)
		}
	}
}
func TestSidebarLegacyIdentityPinAliasIsAuthorizedReadOnlyAndAtomic(t *testing.T) {
	s := boot(t)
	u := s.login("quick-alias@example.test", "admin", "en")
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "site-builder", Href: "/admin/site-design", LabelKey: "Design", Permission: "settings.manage", Group: "appearance"})
	for i, id := range []string{"builder.identity", "site-builder"} {
		if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_dashboard_pins(user_id,nav_id,position,created_at) VALUES(?,?,?,'fixture')`, u.ID, id, i); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.app.Deps.Settings.Set(context.Background(), "site.workspace", "full"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO user_navigation_preferences(user_id,hidden_json,item_order_json,group_order_json,updated_at) VALUES(?,?,'["roles"]','["system"]','fixture') ON CONFLICT(user_id) DO UPDATE SET hidden_json=excluded.hidden_json,item_order_json=excluded.item_order_json,group_order_json=excluded.group_order_json`, u.ID, `["builder.identity","site-builder","menus"]`); e != nil {
		t.Fatal(e)
	}
	data := quickPayload(t, s, "/admin/dashboard/pins")
	html := data["quick_html"].(string)
	if strings.Count(html, `data-quick-id="site-builder"`) != 1 {
		t.Fatal(html)
	}
	mustNotContain(t, html, "/admin/appearance/identity")
	mustContain(t, html, `href="/admin/site-design"`)
	var count int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count)
	if count != 2 {
		t.Fatal("GET migrated physical aliases")
	}
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"builder.identity"}}, 200)
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count)
	if count != 2 {
		t.Fatal("alias add duplicated rows")
	}
	pinPost(t, s, "/admin/dashboard/pins/order", url.Values{"nav_id": {"site-builder"}}, 200)
	// Revocation hides the alias as well as the canonical destination.
	if _, e := s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, u.ID); e != nil {
		t.Fatal(e)
	}
	if e := auth.ReloadRoles(context.Background(), s.app.Deps.DB); e != nil {
		t.Fatal(e)
	}
	data = quickPayload(t, s, "/admin/dashboard/pins")
	mustNotContain(t, data["quick_html"].(string), "/admin/site-design", "builder.identity")
	pinPost(t, s, "/admin/dashboard/pins/builder.identity/remove", nil, 200)
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count)
	if count != 0 {
		t.Fatal("explicit remove left aliases")
	}
	var hidden, items, groups string
	s.app.Deps.DB.QueryRow(`SELECT hidden_json,item_order_json,group_order_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&hidden, &items, &groups)
	if strings.Contains(hidden, "site-builder") || strings.Contains(hidden, "builder.identity") || !strings.Contains(hidden, "menus") || items != `["roles"]` || groups != `["system"]` {
		t.Fatal("alias visibility changed unrelated preferences", hidden, items, groups)
	}
}

func TestSidebarQuickAccessRespectsCurrentWorkspaceWithoutDeletingPins(t *testing.T) {
	s := boot(t)
	u := s.login("quick-simple@example.test", "admin", "en")
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"canvases"}}, 200)
	s.app.Deps.Settings.Set(context.Background(), "site.workspace", "simple")
	data := quickPayload(t, s, "/admin/dashboard/pins")
	mustNotContain(t, data["quick_html"].(string), `href="/admin/canvases"`)
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"canvases"}}, 403)
	var count int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count)
	if count != 1 {
		t.Fatal("workspace changed saved placements")
	}
	s.app.Deps.Settings.Set(context.Background(), "site.workspace", "full")
	data = quickPayload(t, s, "/admin/dashboard/pins")
	mustContain(t, data["quick_html"].(string), `href="/admin/canvases"`)
}
