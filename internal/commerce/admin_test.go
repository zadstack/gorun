package commerce

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/i18n"
)

func TestAdminProductsCRUD(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()

	// Customers and editors without the permission are refused.
	e.signInAdmin("ed@example.com", "editor")
	if r, _ := e.do("GET", "/admin/commerce/products", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", r.StatusCode)
	}
	e.signInAdmin("admin@example.com", "admin")
	resp, page := e.do("GET", "/admin/commerce/products", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "/admin/commerce/products/new", `href="/admin/commerce/products"`) // nav item
	resp, page = e.do("GET", "/admin/commerce/products/new", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("new: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, `name="price"`, `name="server_id"`, `value="`+srv.ID+`"`, `name="features"`, `media:open`, `name="seo_title"`)

	// Validation: active without a server mapping, bad price.
	resp, _ = e.adminPost("/admin/commerce/products", url.Values{"name": {"Pro"}, "price": {"12.345"}, "currency": {"AED"}, "active": {"1"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid: %d", resp.StatusCode)
	}
	resp, _ = e.adminPost("/admin/commerce/products", url.Values{"name": {"Pro"}, "price": {"49"}, "currency": {"AED"}, "active": {"1"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("active without mapping: %d", resp.StatusCode)
	}

	form := url.Values{"name": {"Pro Monthly"}, "slug": {"pro-monthly"}, "price": {"49.50"}, "currency": {"aed"}, "active": {"1"},
		"plan_code": {"pro"}, "server_id": {srv.ID}, "access_months": {"3"}, "group_name": {"MCP Servers"}, "sort": {"2"},
		"features": {"Unlimited tools", "", "Priority support"}, "short_description": {"Short"},
		"description": {`<p onclick="x()">Hi<script>alert(1)</script></p>`}, "seo_title": {"Buy Pro"}}
	resp, _ = e.adminPost("/admin/commerce/products", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	p, err := e.svc.Product(ctx, "pro-monthly")
	if err != nil {
		t.Fatal(err)
	}
	if p.PriceMinor != 4950 || p.Currency != "AED" || !p.Active || p.AccessMonths != 3 || p.GroupSlug != "mcp-servers" || p.Sort != 2 ||
		len(p.Features) != 2 || p.SEO["title"] != "Buy Pro" {
		t.Fatalf("product = %+v", p)
	}
	if strings.Contains(p.Description, "script") || strings.Contains(p.Description, "onclick") || !strings.Contains(p.Description, "Hi") {
		t.Fatalf("description not sanitized: %q", p.Description)
	}
	if loc := resp.Header.Get("Location"); loc != "/admin/commerce/products/"+p.ID {
		t.Fatalf("redirect = %s", loc)
	}
	resp, page = e.do("GET", "/admin/commerce/products/"+p.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("edit: %d", resp.StatusCode)
	}
	mustContain(t, page, `value="49.50"`, `value="Pro Monthly"`, "Priority support")

	// Update (full replace through the form), then the shop shows it.
	form.Set("price", "59")
	form.Set("name", "Pro Monthly+")
	if resp, _ = e.adminPost("/admin/commerce/products/"+p.ID, form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	if p2, _ := e.svc.Product(ctx, "pro-monthly"); p2.PriceMinor != 5900 || p2.Name != "Pro Monthly+" {
		t.Fatalf("updated = %+v", p2)
	}
	_, shop := e.do("GET", "/shop", nil)
	mustContain(t, shop, "Pro Monthly&#43;", "MCP Servers", `/product/pro-monthly`)
	// Duplicate slug.
	if resp, _ = e.adminPost("/admin/commerce/products", form); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate slug: %d", resp.StatusCode)
	}
	if n := auditCount(e, "commerce.product.save"); n != 2 {
		t.Fatalf("product.save audits = %d", n)
	}
	// Delete.
	if resp, _ = e.adminPost("/admin/commerce/products/"+p.ID+"/delete", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := e.svc.Product(ctx, "pro-monthly"); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if r, _ := e.do("GET", "/product/pro-monthly", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted product page: %d", r.StatusCode)
	}
	if auditCount(e, "commerce.product.delete") != 1 {
		t.Fatal("delete not audited")
	}
}

func TestServerTestConnection(t *testing.T) {
	e := boot(t)
	e.signInAdmin("admin@example.com", "admin")
	ctx := context.Background()

	// Create through the admin form: the secret is stored encrypted.
	resp, _ := e.adminPost("/admin/commerce/servers", url.Values{"name": {"Fake"}, "base_url": {e.billing.URL + "/"}, "client_id": {testClientID}, "secret": {testServerSecret}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create server: %d", resp.StatusCode)
	}
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/admin/commerce/servers/")
	if enc := e.str(`SELECT secret_enc FROM billing_servers WHERE id = ?`, id); enc == "" || strings.Contains(enc, testServerSecret) {
		t.Fatalf("secret_enc = %q", enc)
	}
	_, page := e.do("GET", "/admin/commerce/servers/"+id, nil)
	mustContain(t, page, `hx-post="/admin/commerce/servers/`+id+`/test"`)
	mustNotContain(t, page, testServerSecret)

	// Test connection lists the plans.
	resp, frag := e.adminPost("/admin/commerce/servers/"+id+"/test", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("test: %d", resp.StatusCode)
	}
	mustContain(t, frag, "pro", "team", "Pro")
	mustNotContain(t, frag, "<html")
	if st := e.str(`SELECT status FROM billing_servers WHERE id = ?`, id); st != ServerOK {
		t.Fatalf("status = %s", st)
	}
	cat, err := e.svc.TestServer(ctx, id)
	if err != nil || len(cat.Plans) != 2 || cat.Plans[0].Code != "pro" {
		t.Fatalf("catalog = %+v, %v", cat, err)
	}
	if n := e.billing.CallCount("GET", "/catalog"); n != 2 {
		t.Fatalf("catalog calls = %d", n)
	}

	// Wrong secret → error shown and recorded.
	if err := e.svc.SaveServer(ctx, Server{ID: id, Name: "Fake", BaseURL: e.billing.URL, ClientID: testClientID, Secret: "wrong"}); err != nil {
		t.Fatal(err)
	}
	_, frag = e.adminPost("/admin/commerce/servers/"+id+"/test", nil)
	mustContain(t, frag, `role="alert"`, "401")
	if st := e.str(`SELECT status FROM billing_servers WHERE id = ?`, id); st != ServerError {
		t.Fatalf("status = %s", st)
	}
	if auditCount(e, "commerce.server.test") != 3 || auditCount(e, "commerce.server.save") != 2 {
		t.Fatal("server actions not audited")
	}

	// Health checks: Stripe + webhook + this server (failing, then cached).
	checks := &[]admin.HealthCheck{}
	if _, err := e.app.Deps.Hooks.Apply(i18n.WithLocale(ctx, "en"), admin.HealthCheckFilter, checks); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range *checks {
		names = append(names, c.Name+"="+c.Status)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"Stripe secret key=warn", "Stripe webhook secret=warn", "Billing server Fake reachable=fail"} {
		if !strings.Contains(got, want) {
			t.Fatalf("health checks = %s (want %s)", got, want)
		}
	}
	for _, c := range *checks {
		if c.Name == "Stripe secret key" && !strings.Contains(c.Detail, "checkout not verified") {
			t.Fatalf("stripe detail = %q", c.Detail)
		}
		if c.Name == "Stripe webhook secret" && !strings.Contains(c.Detail, "delivery not verified") {
			t.Fatalf("webhook detail = %q", c.Detail)
		}
	}

	// Plain http is refused for a non-lab install.
	e.svc.d.Config.AllowPrivateNet = false
	if err := e.svc.SaveServer(ctx, Server{Name: "Bad", BaseURL: "http://billing.example.com", ClientID: "c", Secret: "s"}); err == nil {
		t.Fatal("http server accepted")
	}
	// Secrets never leave through the list.
	list, _ := e.svc.Servers(ctx)
	for _, s := range list {
		if s.Secret != "" || !s.SecretSet {
			t.Fatalf("server leaked or lost its secret: %+v", s)
		}
	}
}

func TestAdminEntitlementActionsAndSettings(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "c@example.com")
	e.pay(id)
	e.drain(JobProvision)
	entID := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id)
	cust := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id)
	e.signInAdmin("admin@example.com", "admin")

	_, page := e.do("GET", "/admin/commerce/orders", nil)
	mustContain(t, page, "#1000", "c@example.com")
	_, page = e.do("GET", "/admin/commerce/orders/"+id, nil)
	mustContain(t, page, "create", "done")
	_, page = e.do("GET", "/admin/commerce/entitlements?status=active", nil)
	mustContain(t, page, "/admin/commerce/entitlements/"+entID+"/suspend")

	if r, _ := e.adminPost("/admin/commerce/entitlements/"+entID+"/suspend", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("suspend: %d", r.StatusCode)
	}
	if a, _ := e.billing.Account(cust); a.Status != "suspended" {
		t.Fatalf("remote = %+v", a)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE id = ?`, entID); st != EntitlementSuspended {
		t.Fatalf("status = %s", st)
	}
	e.adminPost("/admin/commerce/entitlements/"+entID+"/resume", nil)
	if a, _ := e.billing.Account(cust); a.Status != "active" {
		t.Fatalf("remote after resume = %+v", a)
	}
	// Reconcile adopts the server's state.
	e.billing.SetAccountStatus(cust, "suspended")
	e.adminPost("/admin/commerce/entitlements/"+entID+"/reconcile", nil)
	if st := e.str(`SELECT status FROM entitlements WHERE id = ?`, entID); st != EntitlementSuspended {
		t.Fatalf("after reconcile = %s", st)
	}
	e.billing.SetAccountStatus(cust, "active")
	e.adminPost("/admin/commerce/entitlements/"+entID+"/reconcile", nil)
	// An ended period cannot be resumed (the customer buys again instead).
	ended := e.str(`SELECT ends_at FROM entitlements WHERE id = ?`, entID)
	e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = '2020-01-01T00:00:00Z', status = 'expired' WHERE id = ?`, entID)
	if err := e.svc.Resume(context.Background(), entID); err != ErrPeriodEnded {
		t.Fatalf("resume after end: %v", err)
	}
	e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = ?, status = 'active' WHERE id = ?`, ended, entID)
	resp, page := e.adminPost("/admin/commerce/entitlements/"+entID+"/login-link", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "https://billing.example.test/login/") {
		t.Fatalf("login link: %d", resp.StatusCode)
	}
	if n := e.count(`SELECT count(*) FROM provisioning_ops WHERE kind = 'login_link' AND response_json LIKE '%billing.example.test%'`); n != 0 {
		t.Fatal("login link stored")
	}
	for _, a := range []string{"commerce.entitlement.suspend", "commerce.entitlement.resume", "commerce.entitlement.reconcile", "commerce.entitlement.login_link"} {
		if auditCount(e, a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}

	// Settings: currency + encrypted secrets with set/clear.
	_, page = e.do("GET", "/admin/commerce/settings", nil)
	mustContain(t, page, "http://shop.test/webhooks/stripe", `name="stripe_secret_key"`)
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"usd"}, "stripe_secret_key": {"sk_test_admin"}, "stripe_webhook_secret": {"whsec_admin"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("settings: %d", r.StatusCode)
	}
	ctx := context.Background()
	if e.svc.currency(ctx) != "USD" || e.svc.stripeSecretKey(ctx) != "sk_test_admin" || e.svc.webhookSecret(ctx) != "whsec_admin" {
		t.Fatal("settings not applied")
	}
	_, page = e.do("GET", "/admin/commerce/settings", nil)
	mustNotContain(t, page, "sk_test_admin", "whsec_admin")
	e.adminPost("/admin/commerce/settings", url.Values{"currency": {"USD"}, "clear_stripe_secret_key": {"1"}})
	if e.svc.stripeSecretKey(ctx) != "sk_test_config" { // falls back to the config value
		t.Fatalf("after clear = %s", e.svc.stripeSecretKey(ctx))
	}
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"dollars"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad currency: %d", r.StatusCode)
	}
	// Orders need commerce.view_orders; editors are refused.
	e.signInAdmin("ed@example.com", "editor")
	if r, _ := e.do("GET", "/admin/commerce/orders", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("editor orders: %d", r.StatusCode)
	}
}
