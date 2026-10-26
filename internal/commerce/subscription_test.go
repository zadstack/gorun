package commerce

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakePortal struct{ customer, url string }

func (f *fakePortal) Create(_ context.Context, _, customerID, _ string) (string, error) {
	f.customer = customerID
	if f.url == "" {
		f.url = "https://billing.stripe.test/portal"
	}
	return f.url, nil
}

type fakeRefunds struct {
	intent, id, key, idem string
	calls                 int
}

func (f *fakeRefunds) Create(_ context.Context, key, paymentIntent, idempotencyKey string) (string, error) {
	f.intent, f.key, f.idem = paymentIntent, key, idempotencyKey
	f.calls++
	if f.id == "" {
		f.id = "re_test"
	}
	return f.id, nil
}

func TestSubscriptionLifecycle(t *testing.T) {
	e := boot(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	e.svc.now = func() time.Time { return now }
	if err := e.app.Deps.Settings.Set(context.Background(), SettingCurrency, "USD"); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Deps.Settings.Set(context.Background(), SettingGraceDays, "3"); err != nil {
		t.Fatal(err)
	}
	srv := e.server()
	p, err := e.svc.SaveProduct(context.Background(), Product{
		Name: "Pro", Slug: "pro-sub", PriceMinor: 4900, Currency: "AED", Active: true,
		GroupName: "Plans", PlanCode: "pro", ServerID: srv.ID, AccessMonths: 1,
		BillingMode: "subscription", Interval: "month", TrialDays: 14, StripePriceID: "price_default",
		Prices: map[string]CurrencyPrice{"USD": {PriceID: "price_usd_123", AmountMinor: 1000}, "AED": {PriceID: "price_aed_123", AmountMinor: 3700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	one, err := e.svc.SaveProduct(context.Background(), Product{Name: "Once", Slug: "once", PriceMinor: 2500, Currency: "USD", Active: true,
		GroupName: "Plans", PlanCode: "pro", ServerID: srv.ID, AccessMonths: 1})
	if err != nil {
		t.Fatal(err)
	}
	e.checkout(one, "once@example.com")
	if got := e.stripe.last(t); got.Mode != "payment" || got.PriceID != "" || got.TrialDays != 0 {
		t.Fatalf("one-time checkout = %+v", got)
	}

	orderID := e.checkout(p, "sub@example.com")
	req := e.stripe.last(t)
	if req.Mode != "subscription" || req.PriceID != "price_usd_123" || req.AmountMinor != 1000 || req.Currency != "USD" || req.Interval != "month" || req.TrialDays != 14 || req.Plan != "pro" || req.AccountID == "" || req.SiteID == "" {
		t.Fatalf("subscription checkout = %+v", req)
	}
	if req.CustomerID != "" {
		t.Fatalf("first checkout should not reuse a customer, got %s", req.CustomerID)
	}

	body, header := stripeEvent("evt_sub_paid", evCheckoutCompleted, map[string]any{
		"id": "cs_test_" + orderID, "object": "checkout.session", "client_reference_id": orderID,
		"payment_status": "paid", "payment_intent": "pi_" + orderID, "customer": "cus_sub", "mode": "subscription",
		"amount_total": 1000, "currency": "usd", "metadata": map[string]any{"order_id": orderID},
	})
	if r := e.webhook(body, header); r.StatusCode != 200 {
		t.Fatalf("paid webhook: %d", r.StatusCode)
	}
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	if e.str(`SELECT status FROM orders WHERE id = ?`, orderID) != OrderActive {
		t.Fatalf("order status = %s", e.str(`SELECT status FROM orders WHERE id = ?`, orderID))
	}
	if e.str(`SELECT stripe_customer_id FROM customers WHERE id = (SELECT customer_id FROM orders WHERE id = ?)`, orderID) != "cus_sub" {
		t.Fatal("stripe customer was not stored")
	}
	again := e.checkout(p, "sub@example.com")
	if e.stripe.last(t).CustomerID != "cus_sub" || again == orderID {
		t.Fatalf("customer not reused on %s", again)
	}

	sub := func(id, status string, extra map[string]any) {
		t.Helper()
		obj := map[string]any{"id": "sub_1", "object": "subscription", "customer": "cus_sub", "status": status,
			"cancel_at_period_end": false, "current_period_end": now.Add(30 * 24 * time.Hour).Unix(),
			"metadata": map[string]any{"account_id": req.AccountID, "plan": "pro", "site_id": req.SiteID, "order_id": orderID},
			"items":    map[string]any{"data": []any{map[string]any{"price": map[string]any{"id": "price_usd_123", "currency": "usd"}}}},
		}
		for k, v := range extra {
			obj[k] = v
		}
		b, h := stripeEvent(id, evSubCreated, obj)
		if id != "evt_sub_created" {
			b, h = stripeEvent(id, evSubUpdated, obj)
		}
		if r := e.webhook(b, h); r.StatusCode != 200 {
			t.Fatalf("%s: %d", id, r.StatusCode)
		}
	}
	sub("evt_sub_created", "trialing", nil)
	replayBody, replayHeader := stripeEvent("evt_sub_created", evSubCreated, map[string]any{"id": "sub_1", "customer": "cus_sub", "status": "trialing"})
	if r := e.webhook(replayBody, replayHeader); r.StatusCode != 200 {
		t.Fatalf("replay: %d", r.StatusCode)
	}
	if e.count(`SELECT count(*) FROM subscriptions WHERE stripe_subscription_id = 'sub_1' AND status = 'trialing'`) != 1 {
		t.Fatal("subscription row missing")
	}
	if err := e.svc.RequirePlan(context.Background(), req.AccountID, "pro"); err != nil {
		t.Fatal(err)
	}
	if err := RequirePlan(context.Background(), e.app.Deps, req.AccountID, "missing"); err != ErrPlanRequired {
		t.Fatalf("missing plan: %v", err)
	}

	for _, ev := range []struct{ id, typ string }{
		{"evt_inv_paid", evInvoicePaid},
		{"evt_inv_fail", evInvoicePaymentFailed},
		{"evt_trial", evSubTrialWillEnd},
	} {
		obj := map[string]any{"id": "in_" + ev.id, "object": "invoice", "customer": "cus_sub", "subscription": "sub_1",
			"status": "open", "hosted_invoice_url": "https://pay.stripe.test/in_" + ev.id, "invoice_pdf": "https://pay.stripe.test/in_" + ev.id + ".pdf",
			"currency": "usd", "amount_due": 1000, "amount_paid": 0}
		typ := ev.typ
		if ev.typ == evSubTrialWillEnd {
			obj = map[string]any{"id": "sub_1", "object": "subscription", "customer": "cus_sub", "status": "trialing"}
			typ = ev.typ
		}
		if ev.typ == evInvoicePaid {
			obj["status"] = "paid"
			obj["amount_paid"] = 1000
		}
		b, h := stripeEvent(ev.id, typ, obj)
		if r := e.webhook(b, h); r.StatusCode != 200 {
			t.Fatalf("%s: %d", ev.id, r.StatusCode)
		}
	}
	if e.str(`SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_1'`) != "past_due" {
		t.Fatalf("status after failure = %s", e.str(`SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_1'`))
	}
	if e.str(`SELECT status FROM entitlements WHERE order_id = ?`, orderID) != EntitlementActive {
		t.Fatal("grace should keep access")
	}
	if e.count(`SELECT count(*) FROM invoices WHERE stripe_invoice_id = 'in_evt_inv_paid'`) != 1 {
		t.Fatal("invoice not stored")
	}
	if errs := e.drain(JobBillingMail); len(errs) > 0 || len(e.mail.all()) == 0 {
		t.Fatalf("reminder mail errs=%v sent=%d", errs, len(e.mail.all()))
	}

	e.svc.now = func() time.Time { return now.Add(4 * 24 * time.Hour) }
	if err := e.svc.expireGrace(context.Background()); err != nil {
		t.Fatal(err)
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}
	acct, ok := e.billing.Account(e.str(`SELECT external_id FROM entitlements WHERE order_id = ?`, orderID))
	if !ok || acct.Status != "suspended" {
		t.Fatalf("billing account after grace = %+v %v", acct, ok)
	}
	if err := e.svc.RequirePlan(context.Background(), req.AccountID, "pro"); err != ErrPlanRequired {
		t.Fatalf("suspended plan: %v", err)
	}

	b, h := stripeEvent("evt_inv_resume", evInvoicePaid, map[string]any{"id": "in_resume", "customer": "cus_sub", "subscription": "sub_1",
		"status": "paid", "hosted_invoice_url": "https://pay.stripe.test/in_resume", "invoice_pdf": "https://pay.stripe.test/in_resume.pdf",
		"currency": "usd", "amount_paid": 1000})
	if r := e.webhook(b, h); r.StatusCode != 200 {
		t.Fatalf("resume invoice: %d", r.StatusCode)
	}
	if errs := e.drain(JobResume); len(errs) > 0 {
		t.Fatal(errs)
	}
	acct, _ = e.billing.Account(acct.ExternalID)
	if acct.Status != "active" {
		t.Fatalf("resumed account = %s", acct.Status)
	}
	if err := e.svc.RequirePlan(context.Background(), req.AccountID, "pro"); err != nil {
		t.Fatal(err)
	}

	b, h = stripeEvent("evt_sub_deleted", evSubDeleted, map[string]any{"id": "sub_1", "customer": "cus_sub", "status": "canceled",
		"metadata": map[string]any{"order_id": orderID, "account_id": req.AccountID, "plan": "pro"}})
	if r := e.webhook(b, h); r.StatusCode != 200 {
		t.Fatalf("deleted: %d", r.StatusCode)
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}
	if e.str(`SELECT status FROM entitlements WHERE order_id = ?`, orderID) != EntitlementSuspended {
		t.Fatal("cancel did not suspend")
	}
	for _, want := range []string{"subscription.activated", "subscription.past_due", "subscription.canceled"} {
		if e.count(`SELECT count(*) FROM jobs WHERE kind = 'hooks.event' AND payload_json LIKE ?`, "%"+want+"%") == 0 {
			t.Fatalf("missing hook job %s", want)
		}
	}
	if e.count(`SELECT count(*) FROM jobs WHERE kind = 'hooks.event' AND payload_json LIKE '%"reason":"grace_expired"%'`) != 1 {
		t.Fatal("grace expiry did not emit subscription.canceled")
	}
}

func TestBillingPortalRefundAndTestMode(t *testing.T) {
	e := boot(t)
	portal := &fakePortal{}
	refunds := &fakeRefunds{}
	e.svc.portal = portal
	e.svc.refunds = refunds
	srv := e.server()
	p := e.product("refund-me", srv)
	orderID := e.checkout(p, "refund@example.com")
	e.pay(orderID)
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	e.signInAdmin("admin@example.com", "admin")
	if r, page := e.do("GET", "/admin/commerce/setup", nil); r.StatusCode != 200 || !strings.Contains(page, "/webhooks/stripe/test") {
		t.Fatalf("setup: %d\n%s", r.StatusCode, page)
	}
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"test"}, "stripe_test_secret_key": {"sk_test_only"}, "stripe_test_webhook_secret": {"whsec_test_only"}, "grace_days": {"5"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("save test mode: %d", r.StatusCode)
	}
	if e.svc.stripeMode(context.Background()) != "test" || e.svc.stripeSecretKey(context.Background()) != "sk_test_only" || e.svc.graceDays(context.Background()) != 5 {
		t.Fatal("test mode settings were not stored")
	}
	if r, _ := e.adminPost("/admin/commerce/settings/test-event", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("test event: %d", r.StatusCode)
	}
	if e.count(`SELECT count(*) FROM stripe_events WHERE type = 'customer.subscription.updated'`) != 1 {
		t.Fatal("test event was not recorded")
	}
	body, header := stripeEvent("evt_mode_test", evSubUpdated, map[string]any{"id": "sub_mode", "customer": "cus_x", "status": "active"})
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/webhooks/stripe/test", strings.NewReader(string(body)))
	req.Header.Set("Stripe-Signature", header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("live webhook secret was accepted on the test path")
	}

	if r, _ := e.adminPost("/admin/commerce/orders/"+orderID+"/refund", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("refund: %d", r.StatusCode)
	}
	if refunds.intent != "pi_"+orderID || refunds.idem != "refund:"+orderID || e.str(`SELECT status FROM orders WHERE id = ?`, orderID) != OrderRefunded {
		t.Fatalf("refund result intent=%s idem=%s status=%s", refunds.intent, refunds.idem, e.str(`SELECT status FROM orders WHERE id = ?`, orderID))
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}

	e.signInCustomer(e.str(`SELECT user_id FROM customers WHERE id = (SELECT customer_id FROM orders WHERE id = ?)`, orderID))
	token := e.csrfFrom("/account")
	r, page := e.do("POST", "/account/billing-portal", url.Values{"_csrf": {token}})
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != portal.url || portal.customer != "cus_1" {
		t.Fatalf("portal: %d %s customer %q\n%s", r.StatusCode, r.Header.Get("Location"), portal.customer, page)
	}
}
