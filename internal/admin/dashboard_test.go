package admin_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestDashboardRequiresLogin(t *testing.T) {
	s := boot(t)
	resp, _ := s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login?next=%2Fadmin" {
		t.Fatalf("anonymous dashboard: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	s.createUser("admin@example.com", "admin", "en")
	s.c = newClient()
	_, page := s.do("GET", "/admin/login", nil)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no csrf on login page:\n%s", page)
	}
	resp, _ = s.do("POST", "/admin/login", url.Values{"_csrf": {m[1]}, "email": {"admin@example.com"}, "password": {testPassword}})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || loc != "/admin" {
		t.Fatalf("login: %d %q", resp.StatusCode, loc)
	}
	// The post-login redirect lands on the admin module's dashboard.
	resp, page = s.do("GET", loc, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-widget="recent_audit"`, `data-widget="jobs_summary"`, `data-widget="system_summary"`, "user.login", "<aside")
	if resp, _ := s.do("GET", "/admin/dashboard", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("old /admin/dashboard path should be gone: %d", resp.StatusCode)
	}
}

func TestDashboardHidesSystemWidgetsWithoutPermission(t *testing.T) {
	s := boot(t)
	s.login("ed@example.com", "editor", "en")
	resp, page := s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard: %d", resp.StatusCode)
	}
	mustNotContain(t, page, `data-widget="recent_audit"`, `data-widget="system_summary"`)
}

func TestSidebarRTLWhenArabic(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "ar")
	resp, page := s.do("GET", "/admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin: %d", resp.StatusCode)
	}
	mustContain(t, page, `dir="rtl"`, "الكانفس والوكلاء", "الإعدادات")
	_, page = s.do("GET", "/admin/users", nil)
	mustContain(t, page, "المستخدمون", "الفريق والأدوار")

	s.login("en@example.com", "admin", "en")
	_, page = s.do("GET", "/admin/users", nil)
	mustContain(t, page, `dir="ltr"`, ">Users<", "Team &amp; roles")
	mustNotContain(t, page, "المستخدمون")
}

func TestAgentsNavGroup(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	_, page := s.do("GET", "/admin", nil)
	// Only section destinations occupy the primary sidebar. The detailed
	// key and audit links live in the corresponding contextual page tabs.
	mustContain(t, page, `data-navigation-section="canvas"`, `data-navigation-section="settings"`)
	_, page = s.do("GET", "/admin/agents/keys", nil)
	mustContain(t, page, `data-navigation-context="canvas"`, `href="/admin/agents/keys"`)
	mustNotContain(t, page, `href="/admin/agents/audit"`)
	_, page = s.do("GET", "/admin/agents/audit", nil)
	mustContain(t, page, `data-navigation-context="settings"`, `href="/admin/agents/audit"`)
}
