package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// System → Modules renders the catalog for an admin in both locales and
// refuses to switch a module that is always on.
func TestModulesPageRendersCatalogAndRefusesAlwaysOn(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	resp, page := s.do(http.MethodGet, "/admin/system/modules", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("modules page: %d", resp.StatusCode)
	}
	for _, want := range []string{`data-module="admin"`, `data-module="auth"`, `data-module="mail"`, `data-kind="service"`, "Always on", "No external extensions installed", "Core services", "Built-in modules"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "Turn off at next restart") {
		t.Error("an always-on module offers a switch")
	}
	resp, _ = s.post("/admin/system/modules/admin/activation", url.Values{"enabled": {"false"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "error=not_optional") {
		t.Fatalf("switching admin: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = s.post("/admin/system/modules/nope/activation", url.Values{"enabled": {"false"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "error=not_found") {
		t.Fatalf("switching unknown: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page = s.do(http.MethodGet, "/admin/system/modules?error=not_optional", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "cannot be switched") {
		t.Fatalf("error flash: %d", resp.StatusCode)
	}

	ar := boot(t)
	ar.login("admin@example.com", "admin", "ar")
	resp, page = ar.do(http.MethodGet, "/admin/system/modules", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "الوحدات المدمجة") || !strings.Contains(page, `dir="rtl"`) {
		t.Fatalf("arabic modules page: %d", resp.StatusCode)
	}

	// Editors (no system.health) cannot see it.
	e := boot(t)
	e.login("editor@example.com", "editor", "en")
	if resp, _ := e.do(http.MethodGet, "/admin/system/modules", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}
