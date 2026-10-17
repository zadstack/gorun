package admin_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

func pinPost(t *testing.T, s *site, path string, form url.Values, want int) {
	t.Helper()
	r, _ := s.post(path, form)
	if r.StatusCode != want {
		t.Fatalf("pin %s: %d want%d", path, r.StatusCode, want)
	}
}
func TestDashboardPinsAtomicVisibilityOrderAndOwner(t *testing.T) {
	s := boot(t)
	u := s.login("pins@example.com", "admin", "en")
	pinPost(t, s, "/admin/navigation", url.Values{"intent": {"save"}, "available": {"users", "dashboard"}, "visible": {"users"}, "order_present": {"1"}, "item_order": {"users", "dashboard"}, "group_order": {"settings", ""}}, 303)
	var beforeItems, beforeGroups string
	if e := s.app.Deps.DB.QueryRow(`SELECT item_order_json,group_order_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&beforeItems, &beforeGroups); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}, 200)
	}
	var count int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate", count, e)
	}
	var hidden, items, groups string
	if e := s.app.Deps.DB.QueryRow(`SELECT hidden_json,item_order_json,group_order_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&hidden, &items, &groups); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(hidden, "users") || !strings.Contains(hidden, "dashboard") || items != beforeItems || groups != beforeGroups {
		t.Fatal("prefs overwritten")
	}
	_, page := s.do("GET", "/admin/navigation", nil)
	if strings.Contains(page, `href="/admin/users"`) {
		t.Fatal("pin still in sidebar")
	}
	if r, _ := s.do("GET", "/admin/users", nil); r.StatusCode != 200 {
		t.Fatal("pin changedauthorization")
	}
	s.login("pins-other@example.com", "admin", "en")
	pinPost(t, s, "/admin/dashboard/pins/users/remove", nil, 200)
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("otherownerremoved")
	}
	s.signIn(u.Email)
	pinPost(t, s, "/admin/dashboard/pins/users/remove", nil, 200)
	if e := s.app.Deps.DB.QueryRow(`SELECT hidden_json,item_order_json,group_order_json FROM user_navigation_preferences WHERE user_id=?`, u.ID).Scan(&hidden, &items, &groups); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(hidden, "users") || !strings.Contains(hidden, "dashboard") || items != beforeItems || groups != beforeGroups {
		t.Fatal("restore changedotherprefs")
	}
}
func TestDashboardPinsValidationReorderAndRollback(t *testing.T) {
	s := boot(t)
	u := s.login("pins-order@example.com", "admin", "en")
	if r, _ := s.do("POST", "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}); r.StatusCode != 403 {
		t.Fatal("csrf", r.StatusCode)
	}
	for _, id := range []string{"dashboard", "unknown", "https://evil.test"} {
		pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {id}}, 403)
	}
	for _, id := range []string{"users", "canvases"} {
		pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {id}}, 200)
	}
	pinPost(t, s, "/admin/dashboard/pins/order", url.Values{"nav_id": {"canvases", "users"}}, 200)
	var first string
	if e := s.app.Deps.DB.QueryRow(`SELECT nav_id FROM admin_dashboard_pins WHERE user_id=? ORDER BY position LIMIT 1`, u.ID).Scan(&first); e != nil || first != "canvases" {
		t.Fatal("reorder", first, e)
	}
	pinPost(t, s, "/admin/dashboard/pins/order", url.Values{"nav_id": {"users", "users"}}, 422)
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "pins.failure", Href: "/admin/pins-failure", LabelKey: "Failure"})
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"pins.failure"}}, 200)
	pinPost(t, s, "/admin/navigation", url.Values{"intent": {"save"}}, 303)
	if _, e := s.app.Deps.DB.Exec(`CREATE TRIGGER pin_failure BEFORE UPDATE ON user_navigation_preferences BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); e != nil {
		t.Fatal(e)
	}
	pinPost(t, s, "/admin/dashboard/pins/pins.failure/remove", nil, 500)
	var count int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=? AND nav_id='pins.failure'`, u.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("partialunpincommitted", count, e)
	}
	s.login("pins-editor@example.com", "editor", "en")
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}, 403)
}

func TestDashboardPinsOverlaySurvivesPreferencesAndRevocation(t *testing.T) {
	s := boot(t)
	u := s.login("pins-overlay@example.com", "admin", "en")
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}, 200)
	for _, f := range []url.Values{{"intent": {"save"}, "available": {"users", "dashboard"}, "visible": {"users", "dashboard"}}, {"intent": {"reset"}}} {
		pinPost(t, s, "/admin/navigation", f, 303)
		_, page := s.do("GET", "/admin/navigation", nil)
		start := strings.Index(page, "data-admin-navigation")
		end := strings.Index(page[start:], "</nav>")
		if strings.Contains(page[start:start+end], `href="/admin/users"`) {
			t.Fatal("navigation preference displaced pin")
		}
	}
	s.login("pins-manager@example.com", "admin", "en")
	pinPost(t, s, "/admin/users/"+u.ID, url.Values{"name": {"Editor"}, "email": {u.Email}, "role": {"editor"}, "locale": {"en"}, "status": {"active"}}, 303)
	s.signIn(u.Email)
	_, page := s.do("GET", "/admin", nil)
	if strings.Contains(page, `href="/admin/users"`) {
		t.Fatal("revoked pin leaked source")
	}
	pinPost(t, s, "/admin/dashboard/pins/users/remove", nil, 200)
	var n int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("stale own pin cannotberemoved", n, e)
	}
}

func TestDashboardPinLimitAndIdempotentDuplicate(t *testing.T) {
	s := boot(t)
	u := s.login("pins-limit@example.com", "admin", "en")
	for i := range 50 {
		if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_dashboard_pins(user_id,nav_id,position,created_at) VALUES(?,?,?,?)`, u.ID, fmt.Sprintf("extra.%d", i), i, "2026-10-12T00:00:00Z"); e != nil {
			t.Fatal(e)
		}
	}
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"users"}}, 422)
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "extra.0", Href: "/admin/example", LabelKey: "Example"})
	pinPost(t, s, "/admin/dashboard/pins", url.Values{"nav_id": {"extra.0"}}, 200)
	var n int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_dashboard_pins WHERE user_id=?`, u.ID).Scan(&n); e != nil || n != 50 {
		t.Fatal("limitchanged", n, e)
	}
}
