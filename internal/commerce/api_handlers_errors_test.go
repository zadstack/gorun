package commerce

import (
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// TestAPIProductAndServerNotFoundAndValidation covers product delete,
// replacement of unknown ids, server validation and testing unknown or
// unreachable servers over /api/v1/commerce.
func TestAPIProductAndServerNotFoundAndValidation(t *testing.T) {
	e := boot(t)
	c := apitest.NewClient(t, e.app)
	p := serviceProduct(t, e, "api-delete-me", 100)

	if st, body := c.Delete("/v1/commerce/products/"+p.ID, nil); st != 200 || apitest.Data(body)["deleted"] != true || apitest.Data(body)["id"] != p.ID {
		t.Fatalf("delete product: %d %v", st, body)
	}
	if n := e.count(`SELECT count(*) FROM products WHERE id = ?`, p.ID); n != 0 {
		t.Fatalf("product still stored: %d", n)
	}
	if st, _ := c.Delete("/v1/commerce/products/"+p.ID, nil); st != 404 {
		t.Fatalf("delete twice: %d", st)
	}
	// Replacing a product that does not exist is a 404, not a create.
	st, body := c.Post("/v1/commerce/products", map[string]any{"id": "missing-product", "kind": ProductService, "name": "Ghost", "currency": "AED"})
	if st != 404 {
		t.Fatalf("replace unknown product: %d %v", st, body)
	}
	if n := e.countNonExampleProducts(); n != 0 {
		t.Fatalf("unknown replace created a product: %d", n)
	}
	if st, body = c.Post("/v1/commerce/products", "{not json"); st != 400 {
		t.Fatalf("malformed product body: %d %v", st, body)
	}

	// Server validation: missing fields are reported, nothing is stored.
	st, body = c.Post("/v1/commerce/servers", map[string]any{"name": "", "base_url": "ftp://x", "client_id": ""})
	if st != 422 || apitest.ErrorCode(body) != "validation_failed" {
		t.Fatalf("invalid server: %d %v", st, body)
	}
	if st, body = c.Post("/v1/commerce/servers", map[string]any{"id": "nope", "name": "X", "base_url": e.billing.URL, "client_id": testClientID}); st != 404 {
		t.Fatalf("update unknown server: %d %v", st, body)
	}
	if st, body = c.Post("/v1/commerce/servers", "[]"); st != 400 {
		t.Fatalf("malformed server body: %d %v", st, body)
	}
	if n := e.count(`SELECT count(*) FROM billing_servers`); n != 0 {
		t.Fatalf("invalid servers stored: %d", n)
	}
	if st, _ = c.Post("/v1/commerce/servers/nope/test", nil); st != 404 {
		t.Fatalf("test unknown server: %d", st)
	}

	// A failing billing server reports ok=false with the error and records it.
	srv := e.server()
	e.billing.FailNext("GET", "/catalog", 500, 5)
	st, body = c.Post("/v1/commerce/servers/"+srv.ID+"/test", nil)
	if st != 200 || apitest.Data(body)["ok"] != false || apitest.Data(body)["error"] == "" {
		t.Fatalf("failing server test: %d %v", st, body)
	}
	if got := e.str(`SELECT status FROM billing_servers WHERE id = ?`, srv.ID); got != ServerError {
		t.Fatalf("server status = %q", got)
	}
}

// TestAPIManualRequestLifecycle covers POST /requests, GET /orders/{id} and
// PATCH /requests/{id} including validation and unknown ids.
func TestAPIManualRequestLifecycle(t *testing.T) {
	e := boot(t)
	c := apitest.NewClient(t, e.app)
	p := serviceProduct(t, e, "api-service", 2500)

	if st, body := c.Post("/v1/commerce/requests", "{"); st != 400 {
		t.Fatalf("malformed request: %d %v", st, body)
	}
	st, body := c.Post("/v1/commerce/requests", map[string]any{"items": []map[string]any{{"product_id": p.ID, "quantity": 1}}, "email": "not-an-email", "idempotency_key": "abcdefghijklmnop-api"})
	if st != 422 || apitest.ErrorCode(body) != "validation_failed" {
		t.Fatalf("invalid email request: %d %v", st, body)
	}
	st, body = c.Post("/v1/commerce/requests", map[string]any{"items": []map[string]any{{"product_id": p.ID, "quantity": 2}}, "email": "Buyer@Example.test", "name": " Buyer ", "idempotency_key": "abcdefghijklmnop-api"})
	if st != 200 {
		t.Fatalf("submit request: %d %v", st, body)
	}
	o := apitest.Data(body)
	id, _ := o["id"].(string)
	if o["status"] != OrderPending || o["total_minor"] != float64(5000) || o["email"] != "buyer@example.test" {
		t.Fatalf("order = %v", o)
	}

	st, body = c.Get("/v1/commerce/orders/" + id)
	if st != 200 {
		t.Fatalf("order detail: %d %v", st, body)
	}
	items, _ := apitest.Data(body)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["quantity"] != float64(2) || items[0].(map[string]any)["product_id"] != p.ID {
		t.Fatalf("items = %v", items)
	}
	if st, _ = c.Get("/v1/commerce/orders/missing"); st != 404 {
		t.Fatalf("unknown order: %d", st)
	}

	if st, _ = c.Patch("/v1/commerce/requests/"+id, "nope"); st != 400 {
		t.Fatalf("malformed update: %d", st)
	}
	if st, body = c.Patch("/v1/commerce/requests/"+id, map[string]any{"status": "paid"}); st != 422 {
		t.Fatalf("invalid status: %d %v", st, body)
	}
	if st, _ = c.Patch("/v1/commerce/requests/missing", map[string]any{"status": "accepted"}); st != 404 {
		t.Fatalf("unknown request: %d", st)
	}
	st, body = c.Patch("/v1/commerce/requests/"+id, map[string]any{"status": "accepted", "note": "on it"})
	if st != 200 || apitest.Data(body)["request_status"] != "accepted" {
		t.Fatalf("accept: %d %v", st, body)
	}
	// Fulfilment tracking never asserts payment.
	if got := e.str(`SELECT status FROM orders WHERE id = ?`, id); got != OrderPending {
		t.Fatalf("order status after accept = %q", got)
	}
}

// TestAPIAccountSettingsReadWrite covers the registration policy endpoints.
func TestAPIAccountSettingsReadWrite(t *testing.T) {
	e := boot(t)
	c := apitest.NewClient(t, e.app)
	if st, body := c.Get("/v1/commerce/account-settings"); st != 200 || apitest.Data(body)["registration_enabled"] != false {
		t.Fatalf("read default: %d %v", st, body)
	}
	if st, _ := c.Patch("/v1/commerce/account-settings", "{"); st != 400 {
		t.Fatalf("malformed: %d", st)
	}
	if st, body := c.Patch("/v1/commerce/account-settings", map[string]any{"registration_enabled": true}); st != 200 || apitest.Data(body)["registration_enabled"] != true {
		t.Fatalf("enable: %d %v", st, body)
	}
	if !e.svc.registrationEnabled(t.Context()) {
		t.Fatal("registration not enabled")
	}
	if auditCount(e, "commerce.account_settings.save") != 1 {
		t.Fatal("settings change not audited")
	}
	if st, body := c.Get("/v1/commerce/account-settings"); st != 200 || apitest.Data(body)["registration_enabled"] != true {
		t.Fatalf("read back: %d %v", st, body)
	}
}

// TestAPIEntitlementActionsOutcomes covers suspend/resume/reconcile success,
// an ended period (409) and billing server failures (502).
func TestAPIEntitlementActionsOutcomes(t *testing.T) {
	e := boot(t)
	c := apitest.NewClient(t, e.app)
	id := e.checkout(e.product("api-ent", e.server()), "ent@example.test")
	e.pay(id)
	if errs := e.drain(JobProvision); len(errs) != 0 {
		t.Fatal(errs)
	}
	entID := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id)
	base := "/v1/commerce/entitlements/" + entID + "/"

	st, body := c.Post(base+"suspend", nil)
	ent, _ := apitest.Data(body)["entitlement"].(map[string]any)
	if st != 200 || ent["status"] != EntitlementSuspended {
		t.Fatalf("suspend: %d %v", st, body)
	}
	st, body = c.Post(base+"resume", nil)
	ent, _ = apitest.Data(body)["entitlement"].(map[string]any)
	if st != 200 || ent["status"] != EntitlementActive {
		t.Fatalf("resume: %d %v", st, body)
	}
	cust := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id)
	e.billing.SetAccountStatus(cust, "suspended")
	st, body = c.Post(base+"reconcile", nil)
	ent, _ = apitest.Data(body)["entitlement"].(map[string]any)
	if st != 200 || ent["status"] != EntitlementSuspended {
		t.Fatalf("reconcile: %d %v", st, body)
	}
	if st, _ = c.Post("/v1/commerce/entitlements/missing/resume", nil); st != 404 {
		t.Fatalf("resume unknown: %d", st)
	}
	if st, _ = c.Post("/v1/commerce/entitlements/missing/reconcile", nil); st != 404 {
		t.Fatalf("reconcile unknown: %d", st)
	}

	// A non-retryable billing server answer surfaces as 502.
	e.billing.SetAccountStatus(cust, "active")
	e.billing.FailNext("POST", "/accounts/"+cust+"/suspend", 400, 1)
	st, body = c.Post(base+"suspend", nil)
	if st != http.StatusBadGateway || apitest.ErrorCode(body) != "billing_server_error" {
		t.Fatalf("billing failure: %d %v", st, body)
	}

	// An ended period cannot be resumed.
	if _, err := e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = '2020-01-01T00:00:00Z', status = 'expired' WHERE id = ?`, entID); err != nil {
		t.Fatal(err)
	}
	st, body = c.Post(base+"resume", nil)
	if st != http.StatusConflict || apitest.ErrorCode(body) != "period_ended" {
		t.Fatalf("resume ended: %d %v", st, body)
	}
}

// TestAPIStorageFailuresAre500 makes the commerce tables unavailable and
// checks every listing/mutation reports a server error instead of data.
func TestAPIStorageFailuresAre500(t *testing.T) {
	e := boot(t)
	c := apitest.NewClient(t, e.app)
	p := serviceProduct(t, e, "doomed", 100)
	o, err := e.svc.SubmitCart(t.Context(), CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "x@example.test", IdempotencyKey: "abcdefghijklmnop-storage"})
	if err != nil {
		t.Fatal(err)
	}
	// Order items vanish: the detail cannot be assembled.
	if _, err := e.app.Deps.DB.Exec(`ALTER TABLE order_items RENAME TO order_items_gone`); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.Get("/v1/commerce/orders/" + o.ID); st != 500 {
		t.Fatalf("detail without items table: %d", st)
	}
	for _, tbl := range []string{"products", "orders", "entitlements", "billing_servers"} {
		if _, err := e.app.Deps.DB.Exec(`ALTER TABLE ` + tbl + ` RENAME TO ` + tbl + `_gone`); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/commerce/products", "/v1/commerce/orders", "/v1/commerce/entitlements", "/v1/commerce/servers", "/v1/commerce/orders/" + o.ID} {
		if st, _ := c.Get(path); st != 500 {
			t.Errorf("GET %s: %d, want 500", path, st)
		}
	}
	if st, _ := c.Delete("/v1/commerce/products/"+p.ID, nil); st != 500 {
		t.Errorf("delete: %d", st)
	}
	if st, _ := c.Post("/v1/commerce/entitlements/x/suspend", nil); st != 500 {
		t.Errorf("suspend: %d", st)
	}
	if st, _ := c.Patch("/v1/commerce/requests/"+o.ID, map[string]any{"status": "accepted"}); st != 500 {
		t.Errorf("update request: %d", st)
	}
	st, body := c.Post("/v1/commerce/requests", map[string]any{"items": []map[string]any{{"product_id": p.ID, "quantity": 1}}, "email": "y@example.test", "idempotency_key": "abcdefghijklmnop-storage2"})
	// A storage failure during the product lookup is a server error, not a
	// cart validation error.
	if st != 500 {
		t.Errorf("request without products table: %d, want 500: %v", st, body)
	}
}
