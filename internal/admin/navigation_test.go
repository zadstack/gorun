package admin_test

import (
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestNavigationPreferencesPersistAndDoNotChangeAccess(t *testing.T) {
	s := boot(t)
	u := s.login("nav@example.com", "admin", "en")
	response, page := s.do("GET", "/admin/navigation", nil)
	if response.StatusCode != 200 {
		t.Fatalf("preferences GET: %d", response.StatusCode)
	}
	mustContain(t, page, "Customize sidebar", `value="dashboard"`, `value="users"`)
	response, _ = s.post("/admin/navigation", url.Values{"intent": {"save"}, "available": {"dashboard", "users"}, "visible": {"dashboard"}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", response.StatusCode)
	}
	s.signIn(u.Email)
	_, page = s.do("GET", "/admin", nil)
	mustNotContain(t, page, `href="/admin/users"`)
	mustContain(t, page, `href="/admin/navigation"`)
	response, _ = s.do("GET", "/admin/users", nil)
	if response.StatusCode != 200 {
		t.Fatalf("hiding changed authorization: %d", response.StatusCode)
	}
	s.login("other@example.com", "admin", "en")
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/users"`)
	s.signIn(u.Email)
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "new.module", LabelKey: "New module", Href: "/admin/new-module"})
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/new-module"`)
	s.post("/admin/navigation", url.Values{"intent": {"reset"}})
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/users"`)
}

func TestNavigationRejectsUnauthorizedUnknownAndCSRF(t *testing.T) {
	s := boot(t)
	s.login("nav-editor@example.com", "editor", "ar")
	_, page := s.do("GET", "/admin/navigation", nil)
	mustContain(t, page, `dir="rtl"`, "تخصيص القائمة")
	mustNotContain(t, page, `value="users"`)
	for _, f := range []url.Values{
		{"intent": {"save"}, "available": {"users"}},
		{"intent": {"save"}, "available": {"unknown"}},
		{"intent": {"save"}, "available": {"dashboard"}, "visible": {"users"}},
		{"intent": {"save"}, "available": {"dashboard", "dashboard"}},
		{"intent": {"invalid"}},
	} {
		if res, _ := s.post("/admin/navigation", f); res.StatusCode != 400 {
			t.Fatalf("invalid form accepted: %d", res.StatusCode)
		}
	}
	if res, _ := s.do("POST", "/admin/navigation", url.Values{"intent": {"reset"}}); res.StatusCode != 403 {
		t.Fatalf("csrf: %d", res.StatusCode)
	}
	if res, _ := s.do("GET", "/admin/users", nil); res.StatusCode != 403 {
		t.Fatalf("unauthorized route: %d", res.StatusCode)
	}
}

func TestNavigationAllHiddenKeepsRecoveryAndNewEntriesVisible(t *testing.T) {
	s := boot(t)
	s.login("hidden@example.com", "admin", "en")
	_, page := s.do("GET", "/admin/navigation", nil)
	// IDs from the rendered form are the exact authorized snapshot.
	ids := navigationIDs(page)
	if len(ids) < 2 {
		t.Fatal("navigation snapshot missing")
	}
	if res, _ := s.post("/admin/navigation", url.Values{"intent": {"save"}, "available": ids}); res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/navigation"`)
	start := strings.Index(page, `data-admin-navigation`)
	end := strings.Index(page[start:], "</nav>")
	if strings.Contains(page[start:start+end], "<a ") {
		t.Fatal("hidden navigation still contains items")
	}
	s.post("/admin/navigation", url.Values{"intent": {"reset"}})
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/users"`)
}

func navigationIDs(page string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`name="available" value="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestNavigationOrderingPersistsWithHiddenItemsPermissionsAndReset(t *testing.T) {
	s := boot(t)
	u := s.login("ordered@example.com", "admin", "en")
	for i, id := range []string{"order.alpha", "order.beta"} {
		s.app.Deps.Admin.AddNav(kernel.NavItem{ID: id, LabelKey: id, Href: "/admin/" + id, Group: "system", Position: 2000 + i})
	}
	form := url.Values{"intent": {"save"}, "available": {"dashboard", "order.alpha", "order.beta"}, "visible": {"dashboard", "order.beta"}, "order_present": {"1"}, "item_order": {"order.beta", "order.alpha", "dashboard"}, "group_order": {"system", ""}}
	if r, _ := s.post("/admin/navigation", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	s.signIn(u.Email)
	_, page := s.do("GET", "/admin", nil)
	page = page[strings.Index(page, `data-admin-navigation`):]
	if strings.Index(page, `href="/admin/order.beta"`) > strings.Index(page, `href="/admin"`) {
		t.Fatal("group order did not persist")
	}
	mustNotContain(t, page, `href="/admin/order.alpha"`)
	_, page = s.do("GET", "/admin/navigation", nil)
	if strings.Index(page, `name="item_order" value="order.beta"`) > strings.Index(page, `name="item_order" value="order.alpha"`) || !strings.Contains(page, `name="item_order" value="order.alpha"`) {
		t.Fatal("hidden item lost saved order")
	}
	s.app.Deps.Admin.AddNav(kernel.NavItem{ID: "order.new", LabelKey: "New", Href: "/admin/order.new", Group: "system", Position: 1})
	_, page = s.do("GET", "/admin", nil)
	if strings.Index(page, `href="/admin/order.beta"`) > strings.Index(page, `href="/admin/order.new"`) {
		t.Fatal("new item displaced personal order")
	}
	s.login("isolated-order@example.com", "admin", "en")
	_, page = s.do("GET", "/admin", nil)
	if strings.Index(page, `href="/admin/order.alpha"`) > strings.Index(page, `href="/admin/order.beta"`) {
		t.Fatal("another user's order leaked")
	}
	s.signIn(u.Email)
	s.post("/admin/navigation", url.Values{"intent": {"reset"}})
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/order.alpha"`)
	if strings.Index(page, `href="/admin/order.alpha"`) > strings.Index(page, `href="/admin/order.beta"`) {
		t.Fatal("reset did not reset ordering")
	}
}

func TestNavigationOrderingRejectsInvalidSnapshots(t *testing.T) {
	s := boot(t)
	s.login("order-editor@example.com", "editor", "en")
	for _, f := range []url.Values{
		{"intent": {"save"}, "available": {"dashboard"}, "visible": {"dashboard"}, "order_present": {"1"}, "item_order": {"users"}, "group_order": {""}},
		{"intent": {"save"}, "available": {"dashboard"}, "visible": {"dashboard"}, "order_present": {"1"}, "item_order": {"dashboard", "dashboard"}, "group_order": {""}},
		{"intent": {"save"}, "available": {"dashboard"}, "visible": {"dashboard"}, "order_present": {"1"}, "item_order": {"dashboard"}, "group_order": {"system"}},
		{"intent": {"save"}, "available": {"dashboard"}, "visible": {"dashboard"}, "order_present": {"1"}, "group_order": {""}},
	} {
		if r, _ := s.post("/admin/navigation", f); r.StatusCode != 400 {
			t.Fatalf("invalid order accepted: %d", r.StatusCode)
		}
	}
}

func TestNavigationOrderSurvivesOldFormAndRoleReduction(t *testing.T) {
	s := boot(t)
	u := s.login("role-order@example.com", "admin", "en")
	f := url.Values{"intent": {"save"}, "available": {"dashboard", "users"}, "visible": {"dashboard", "users"}, "order_present": {"1"}, "item_order": {"users", "dashboard"}, "group_order": {"settings", ""}}
	if r, _ := s.post("/admin/navigation", f); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	// A stale pre-order visibility form must not erase the saved order.
	if r, _ := s.post("/admin/navigation", url.Values{"intent": {"save"}, "available": {"dashboard"}, "visible": {"dashboard"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	_, page := s.do("GET", "/admin/navigation", nil)
	if strings.Index(page, `name="item_order" value="users"`) > strings.Index(page, `name="item_order" value="dashboard"`) {
		t.Fatal("legacy form erased order")
	}
	s.login("role-manager@example.com", "admin", "en")
	if r, _ := s.post("/admin/users/"+u.ID, url.Values{"name": {"Editor"}, "email": {u.Email}, "role": {"editor"}, "locale": {"en"}, "status": {"active"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	s.signIn(u.Email)
	_, page = s.do("GET", "/admin/navigation", nil)
	mustNotContain(t, page, `name="item_order" value="users"`, `href="/admin/users"`)
	if r, _ := s.do("GET", "/admin/users", nil); r.StatusCode != 403 {
		t.Fatal("saved order bypassed role", r.StatusCode)
	}
	if r, _ := s.post("/admin/navigation", f); r.StatusCode != 400 {
		t.Fatal("stale unauthorized order accepted", r.StatusCode)
	}
}
