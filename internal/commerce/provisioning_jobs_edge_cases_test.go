package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestProvisionIgnoresMalformedUnknownAndNonProvisioningOrders(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	for _, raw := range []string{`not json`, `{}`, `{"order_id":""}`, `{"order_id":"missing-order"}`} {
		if err := e.svc.provision(ctx, json.RawMessage(raw)); err != nil {
			t.Errorf("provision(%s) = %v, want nil (nothing to retry)", raw, err)
		}
	}
	// A manual service request is never provisioned on a billing server,
	// even if it is marked paid.
	p := serviceProduct(t, e, "svc", 0)
	o, err := e.svc.SubmitCart(ctx, CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "svc@example.test", IdempotencyKey: "abcdefghijklmnop-provision"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Deps.DB.Exec(`UPDATE orders SET status = 'paid', paid_at = '2026-01-01T00:00:00Z', product_snapshot_json = json_set(product_snapshot_json, '$.kind', 'service') WHERE id = ?`, o.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(provisionPayload{OrderID: o.ID})
	if err := e.svc.provision(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM entitlements`); n != 0 {
		t.Fatalf("service order created %d entitlements", n)
	}
	if e.billing.CallCount("POST", "/accounts") != 0 {
		t.Fatal("service order reached the billing server")
	}
	if err := e.svc.RetryProvisioning(ctx, "missing-order"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry unknown order: %v", err)
	}
	var ve *ValidationError
	if err := e.svc.RetryProvisioning(ctx, o.ID); !errors.As(err, &ve) {
		t.Fatalf("retry service order: %v", err)
	}
}

// activeEntitlement provisions a paid order for email and returns the
// entitlement id and customer id.
func activeEntitlement(t *testing.T, e *env, p *Product, email string) (entID, customerID string) {
	t.Helper()
	id := e.checkout(p, email)
	e.pay(id)
	if errs := e.drain(JobProvision); len(errs) != 0 {
		t.Fatal(errs)
	}
	return e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id), e.str(`SELECT customer_id FROM orders WHERE id = ?`, id)
}

func TestSuspendJobSkipsAndSharedAccount(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := e.product("suspendable", e.server())
	ent1, cust := activeEntitlement(t, e, p, "shared@example.test")

	for _, raw := range []string{`nope`, `{}`, `{"entitlement_id":"missing"}`} {
		if err := e.svc.suspendJob(ctx, json.RawMessage(raw)); err != nil {
			t.Errorf("suspendJob(%s) = %v", raw, err)
		}
	}
	// Still active (resumed meanwhile): nothing to do remotely.
	payload, _ := json.Marshal(suspendPayload{EntitlementID: ent1, Reason: "refund"})
	if err := e.svc.suspendJob(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if e.billing.CallCount("POST", "/accounts/"+cust+"/suspend") != 0 {
		t.Fatal("active entitlement suspended remotely")
	}

	// A second active entitlement on the same server keeps the shared account.
	ent2, cust2 := activeEntitlement(t, e, p, "shared@example.test")
	if cust2 != cust || ent2 == ent1 {
		t.Fatalf("expected a second entitlement for the same customer: %s %s", cust2, ent2)
	}
	if _, err := e.app.Deps.DB.Exec(`UPDATE entitlements SET status = 'suspended' WHERE id = ?`, ent1); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.suspendJob(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if e.billing.CallCount("POST", "/accounts/"+cust+"/suspend") != 0 {
		t.Fatal("shared account suspended while another entitlement is active")
	}
	if a, _ := e.billing.Account(cust); a.Status != "active" {
		t.Fatalf("remote account = %+v", a)
	}
}

func TestSuspendJobRetriesTransientAndDefaultsKey(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	entID, cust := activeEntitlement(t, e, e.product("transient", e.server()), "t@example.test")
	if _, err := e.app.Deps.DB.Exec(`UPDATE entitlements SET status = 'expired' WHERE id = ?`, entID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(suspendPayload{EntitlementID: entID, Reason: "expire"})
	path := "/accounts/" + cust + "/suspend"

	// A retryable failure outside the last attempt is returned for a retry
	// and not audited as a final failure.
	e.billing.FailNext("POST", path, 503, 1)
	if err := e.svc.suspendJob(ctx, payload); err == nil {
		t.Fatal("transient failure swallowed")
	}
	if auditCount(e, "commerce.entitlement.suspend_failed") != 0 {
		t.Fatal("transient failure audited as final")
	}
	if err := e.svc.suspendJob(ctx, payload); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if a, _ := e.billing.Account(cust); a.Status != "suspended" {
		t.Fatalf("remote = %+v", a)
	}
	if auditCount(e, "commerce.entitlement.suspended_remote") != 1 {
		t.Fatal("remote suspension not audited")
	}
	// Without an explicit key the operation key is reason:entitlement.
	if n := e.count(`SELECT count(*) FROM provisioning_ops WHERE idempotency_key = ?`, "expire:"+entID); n != 1 {
		t.Fatalf("default key ops = %d", n)
	}
}

func TestDateOnlyFallsBackToRawValue(t *testing.T) {
	if got := dateOnly("2026-03-04T05:06:07Z"); got != "2026-03-04" {
		t.Fatalf("dateOnly(valid) = %q", got)
	}
	if got := dateOnly("not a date"); got != "not a date" {
		t.Fatalf("dateOnly(invalid) = %q", got)
	}
	if got := dateOnly(""); got != "" {
		t.Fatalf("dateOnly(empty) = %q", got)
	}
}
