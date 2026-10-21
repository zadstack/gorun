package commerce

import (
	"net/http"
	"testing"
)

func TestAdminSetupLaunchChecklist(t *testing.T) {
	e := boot(t)
	e.signInAdmin("admin@example.com", "admin")
	resp, page := e.do("GET", "/admin/commerce/setup", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page,
		`/admin/commerce/settings`,
		`/webhooks/stripe`,
		`data-setup-step="stripe"`,
		`data-setup-step="webhook"`,
		`/admin/commerce/servers/new`,
		`/admin/commerce/products/new`,
		`/admin/commerce/products`, // nav includes launch + products
	)
	e.signInAdmin("ed@example.com", "editor")
	if r, _ := e.do("GET", "/admin/commerce/setup", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", r.StatusCode)
	}
}
