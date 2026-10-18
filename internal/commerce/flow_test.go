package commerce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func TestCheckoutCreatesPendingOrderAndUser(t *testing.T) {
	e := boot(t)
	p := e.product("pro-monthly", e.server())

	// Product page renders through the theme with the checkout form.
	resp, page := e.do("GET", "/product/pro-monthly", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("product page: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "Plan pro-monthly", `action="/checkout/pro-monthly"`, `name="email"`, `name="website"`)
	csrf := csrfFieldRe.FindStringSubmatch(page)[1]

	// Missing CSRF is refused.
	if r, _ := e.do("POST", "/checkout/pro-monthly", url.Values{"email": {"buyer@example.com"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("checkout without csrf: %d", r.StatusCode)
	}
	resp, _ = e.do("POST", "/checkout/pro-monthly", url.Values{"_csrf": {csrf}, "email": {" Buyer@Example.com "}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("checkout: %d", resp.StatusCode)
	}
	req := e.stripe.last(t)
	if loc := resp.Header.Get("Location"); loc != "https://checkout.stripe.test/pay/cs_test_"+req.OrderID {
		t.Fatalf("redirect = %s", loc)
	}
	if req.Email != "buyer@example.com" || req.AmountMinor != 4900 || req.Currency != "AED" || req.ProductName != "Plan pro-monthly" {
		t.Fatalf("stripe request = %+v", req)
	}
	if req.SuccessURL != "http://shop.test/checkout/success?o="+req.OrderID+"&s={CHECKOUT_SESSION_ID}" || req.CancelURL != "http://shop.test/product/pro-monthly" {
		t.Fatalf("urls = %s | %s", req.SuccessURL, req.CancelURL)
	}
	if e.stripe.keys[0] != "sk_test_config" {
		t.Fatalf("stripe key = %q", e.stripe.keys[0])
	}

	u, err := e.users().GetByEmail(context.Background(), "buyer@example.com")
	if err != nil || u.Role != auth.RoleCustomer {
		t.Fatalf("customer user = %+v, %v", u, err)
	}
	o, err := e.svc.Order(context.Background(), req.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != OrderPending || o.Email != "buyer@example.com" || o.TotalMinor != 4900 || o.StripeSessionID != "cs_test_"+o.ID || o.Number != "1000" {
		t.Fatalf("order = %+v", o)
	}
	if o.ProductSnapshot["plan_code"] != "pro" || o.ProductName != "Plan pro-monthly" {
		t.Fatalf("snapshot = %+v", o.ProductSnapshot)
	}
	if n := e.count(`SELECT count(*) FROM customers WHERE user_id = ?`, u.ID); n != 1 {
		t.Fatalf("customers = %d", n)
	}
	// A second checkout reuses the user and customer; numbers increase.
	id2 := e.checkout(p, "buyer@example.com")
	if n := e.count(`SELECT count(*) FROM customers`); n != 1 {
		t.Fatalf("customers after second checkout = %d", n)
	}
	if num := e.str(`SELECT number FROM orders WHERE id = ?`, id2); num != "1001" {
		t.Fatalf("second number = %s", num)
	}
	// Bad email → 422 on the product page, no order.
	before := e.count(`SELECT count(*) FROM orders`)
	resp, _ = e.do("POST", "/checkout/pro-monthly", url.Values{"_csrf": {csrf}, "email": {"nope"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || e.count(`SELECT count(*) FROM orders`) != before {
		t.Fatalf("bad email: %d", resp.StatusCode)
	}
	// Honeypot: silently ignored.
	resp, _ = e.do("POST", "/checkout/pro-monthly", url.Values{"_csrf": {csrf}, "email": {"bot@example.com"}, "website": {"x"}})
	if resp.StatusCode != http.StatusSeeOther || e.count(`SELECT count(*) FROM orders`) != before {
		t.Fatalf("honeypot: %d", resp.StatusCode)
	}
	if auditCount(e, "commerce.order.create") != 2 {
		t.Fatalf("order.create audits = %d", auditCount(e, "commerce.order.create"))
	}

	// Success page: allowed via the signed cookie set at checkout.
	resp, page = e.do("GET", "/checkout/success?o="+req.OrderID+"&s=cs_test_"+req.OrderID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("success: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, `id="gp-order-status"`, `data-status="pending"`, `hx-get="/checkout/status/`+req.OrderID+`"`, `hx-trigger="every 3s"`)
	// Another browser without cookie or session id gets 404.
	other := newClient()
	r2, _ := other.Get(e.srv.URL + "/checkout/status/" + req.OrderID)
	if r2.StatusCode != http.StatusNotFound {
		t.Fatalf("status without cookie: %d", r2.StatusCode)
	}
	r3, _ := other.Get(e.srv.URL + "/checkout/success?o=" + req.OrderID + "&s=cs_wrong")
	if r3.StatusCode != http.StatusNotFound {
		t.Fatalf("success with a wrong session id: %d", r3.StatusCode)
	}
}

func TestCheckoutWithoutStripeKeyIsUnavailable(t *testing.T) {
	e := boot(t)
	p := e.product("p1", e.server())
	e.app.Deps.Config.Stripe.SecretKey = "" // Deps.Config is a copy per boot; service reads d.Config
	e.svc.d.Config.Stripe.SecretKey = ""
	if _, _, err := e.svc.StartCheckout(context.Background(), p.ID, "a@example.com"); err != ErrStripeNotConfigured {
		t.Fatalf("err = %v", err)
	}
	// The settings override wins over config.
	if err := e.svc.setSecretSetting(context.Background(), SettingStripeSecret, "sk_test_settings"); err != nil {
		t.Fatal(err)
	}
	if raw := e.str(`SELECT value_json FROM settings WHERE key = ?`, SettingStripeSecret); strings.Contains(raw, "sk_test_settings") {
		t.Fatal("stripe key stored in plain text")
	}
	e.checkout(p, "a@example.com")
	if k := e.stripe.keys[len(e.stripe.keys)-1]; k != "sk_test_settings" {
		t.Fatalf("key = %s", k)
	}
}

func TestWebhookSignatureInvalidRejected(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "a@example.com")
	body, header := stripeEvent("evt_1", evCheckoutCompleted, completedSession(id))

	for name, h := range map[string]string{
		"missing":   "",
		"garbage":   "t=1,v1=deadbeef",
		"tampered":  header,
		"wrong key": strings.Replace(header, "v1=", "v1=00", 1),
	} {
		b := body
		if name == "tampered" {
			b = []byte(strings.Replace(string(body), `"paid"`, `"unpaid"`, 1))
		}
		if r := e.webhook(b, h); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, r.StatusCode)
		}
	}
	if n := e.count(`SELECT count(*) FROM stripe_events`); n != 0 {
		t.Fatalf("rejected events recorded: %d", n)
	}
	if o, _ := e.svc.Order(context.Background(), id); o.Status != OrderPending {
		t.Fatalf("order status = %s", o.Status)
	}
	// No webhook secret configured → unsigned payloads are never accepted.
	e.svc.d.Config.Stripe.WebhookSecret = ""
	if r := e.webhook(body, header); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no secret: %d", r.StatusCode)
	}
}

// TestWebhookDuplicateEventOnce (Review Focus 3): the same event delivered
// twice — and a second, different completion event for the same order —
// pays the order once and enqueues provisioning once.
func TestWebhookDuplicateEventOnce(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "a@example.com")
	body, header := stripeEvent("evt_dup", evCheckoutCompleted, completedSession(id))
	for i := 0; i < 3; i++ {
		if r := e.webhook(body, header); r.StatusCode != 200 {
			t.Fatalf("delivery %d: %d", i, r.StatusCode)
		}
	}
	body2, header2 := stripeEvent("evt_other", evCheckoutAsyncSuccess, completedSession(id))
	if r := e.webhook(body2, header2); r.StatusCode != 200 {
		t.Fatalf("second event: %d", r.StatusCode)
	}
	if n := e.count(`SELECT count(*) FROM stripe_events`); n != 2 {
		t.Fatalf("stripe_events = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobProvision); n != 1 {
		t.Fatalf("provision jobs = %d", n)
	}
	if n := auditCount(e, "commerce.order.paid"); n != 1 {
		t.Fatalf("order.paid audits = %d", n)
	}
	o, _ := e.svc.Order(context.Background(), id)
	if o.Status != OrderPaid || o.PaidAt == "" || o.StripePaymentIntent != "pi_"+id {
		t.Fatalf("order = %+v", o)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'hooks.event' AND payload_json LIKE '%commerce.order.paid%'`); n != 1 {
		t.Fatalf("order.paid events = %d", n)
	}
	// An unpaid completion (delayed payment method) does not pay.
	id2 := e.checkout(e.product("p2", e.server()), "b@example.com")
	obj := completedSession(id2)
	obj["payment_status"] = "unpaid"
	b3, h3 := stripeEvent("evt_unpaid", evCheckoutCompleted, obj)
	e.webhook(b3, h3)
	if o, _ := e.svc.Order(context.Background(), id2); o.Status != OrderPending {
		t.Fatalf("unpaid order = %s", o.Status)
	}
}

func TestWebhookBeforeSuccessRedirectStillOneEntitlement(t *testing.T) {
	e := boot(t)
	p := e.product("p", e.server())
	csrf := e.csrfFrom("/product/p")
	resp, _ := e.do("POST", "/checkout/p", url.Values{"_csrf": {csrf}, "email": {"early@example.com"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("checkout: %d", resp.StatusCode)
	}
	id := e.stripe.last(t).OrderID
	_ = p

	// The webhook (and provisioning) completes before the browser returns.
	e.pay(id)
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	// The browser lands on the success page: it shows the current state.
	resp, page := e.do("GET", "/checkout/success?o="+id+"&s=cs_test_"+id, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("success: %d", resp.StatusCode)
	}
	mustContain(t, page, `data-status="active"`, `href="/account"`)
	mustNotContain(t, page, `hx-trigger="every 3s"`)
	// Polling the status stops (286) and shows active.
	resp, frag := e.do("GET", "/checkout/status/"+id, nil)
	if resp.StatusCode != pollStopStatus || !strings.Contains(frag, `data-status="active"`) {
		t.Fatalf("status: %d %s", resp.StatusCode, frag)
	}
	// Replays of the event and a second provision run change nothing.
	e.pay(id)
	if err := e.svc.provision(context.Background(), json.RawMessage(`{"order_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM entitlements WHERE order_id = ?`, id); n != 1 {
		t.Fatalf("entitlements = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM provisioning_ops WHERE idempotency_key = ?`, "create:"+id); n != 1 {
		t.Fatalf("create ops = %d", n)
	}
	if n := e.billing.CallCount("POST", "/accounts"); n != 1 {
		t.Fatalf("POST /accounts executed %d times", n)
	}
	if n := len(e.mail.all()); n != 1 {
		t.Fatalf("emails = %d", n)
	}
}

func TestProvisionJobActivatesAndEmailsLink(t *testing.T) {
	e := boot(t)
	srv := e.server()
	p := e.product("p", srv)
	id := e.checkout(p, "cust@example.com")
	e.pay(id)
	if n := e.count(`SELECT max_attempts FROM jobs WHERE kind = ?`, JobProvision); n != provisionMaxAttempts {
		t.Fatalf("max_attempts = %d", n)
	}
	before := time.Now().UTC()
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	o, _ := e.svc.Order(context.Background(), id)
	if o.Status != OrderActive {
		t.Fatalf("order status = %s", o.Status)
	}
	ents, _, _ := e.svc.Entitlements(context.Background(), ListQuery{})
	if len(ents) != 1 {
		t.Fatalf("entitlements = %d", len(ents))
	}
	ent := ents[0]
	if ent.Status != EntitlementActive || ent.PlanCode != "pro" || ent.ServerID != srv.ID || ent.ExternalID != o.CustomerID || ent.OrderID != id {
		t.Fatalf("entitlement = %+v", ent)
	}
	end, _ := db.ParseTime(ent.EndsAt)
	if want := addMonths(before, 1); end.Sub(want).Abs() > time.Minute {
		t.Fatalf("ends_at = %s, want ≈ %s", ent.EndsAt, want)
	}
	// Golden body: exactly the fields, order and format of
	// MCBB_WooCommerce::process (mcp-billing-bridge 0.3.1), with the key.
	var key, raw string
	for _, c := range e.billing.Calls() {
		if c.Method == "POST" && c.Path == "/accounts" {
			key, raw = c.IdempotencyKey, c.RawBody
		}
	}
	wantBody := `{"external_customer_id":"` + o.CustomerID + `","order_ref":"gp_` + id + `","plan_code":"pro","period_end":"` + end.UTC().Format("2006-01-02T15:04:05Z") + `"}`
	if key != "create:"+id || raw != wantBody {
		t.Fatalf("POST /accounts key %q body\n%s\nwant\n%s", key, raw, wantBody)
	}
	acct, ok := e.billing.Account(o.CustomerID)
	if !ok || acct.Status != "active" {
		t.Fatalf("remote account = %+v", acct)
	}
	// The customer got an email with a working magic link to /account.
	mails := e.mail.all()
	if len(mails) != 1 || mails[0].To != "cust@example.com" {
		t.Fatalf("mails = %+v", mails)
	}
	m := mails[0]
	i := strings.Index(m.Text, "http://shop.test/auth/magic/")
	if i < 0 || !strings.Contains(m.HTML, "/auth/magic/") || !strings.Contains(m.Subject, "Plan p") {
		t.Fatalf("mail = %+v", m)
	}
	token := strings.Fields(m.Text[i+len("http://shop.test/auth/magic/"):])[0]
	resp, _ := e.do("POST", "/auth/magic/"+token, url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		t.Fatalf("magic link: %d", resp.StatusCode)
	}
	if auditCount(e, "commerce.entitlement.activated") != 1 {
		t.Fatal("activation not audited")
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'hooks.event' AND payload_json LIKE '%commerce.entitlement.activated%'`); n != 1 {
		t.Fatalf("activated events = %d", n)
	}
}

func TestProvisionRetriesOnServerError(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "retry@example.com")
	e.pay(id)
	payload := json.RawMessage(`{"order_id":"` + id + `"}`)
	ctx := context.Background()

	e.billing.FailNext("POST", "/accounts", 503, 2)
	for i := 1; i <= 2; i++ {
		err := e.svc.provision(ctx, payload)
		if err == nil || !retryable(err) {
			t.Fatalf("attempt %d: err = %v (want a retryable error so the job backs off)", i, err)
		}
		if a := e.count(`SELECT attempts FROM provisioning_ops WHERE idempotency_key = ?`, "create:"+id); a != i {
			t.Fatalf("attempts = %d", a)
		}
		if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id); st != EntitlementPending {
			t.Fatalf("entitlement during retries = %s", st)
		}
	}
	if err := e.svc.provision(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id); st != EntitlementActive {
		t.Fatalf("entitlement = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM entitlements`); n != 1 {
		t.Fatalf("entitlements = %d", n)
	}
	// Every attempt used the same idempotency key and the same body.
	var keys, bodies []string
	for _, c := range e.billing.Calls() {
		if c.Path == "/accounts" {
			keys = append(keys, c.IdempotencyKey)
			b, _ := json.Marshal(c.Body)
			bodies = append(bodies, string(b))
		}
	}
	if len(keys) != 3 || keys[0] != keys[2] || bodies[0] != bodies[2] {
		t.Fatalf("keys = %v bodies = %v", keys, bodies)
	}

	// A permanent 4xx fails the order without retrying.
	id2 := e.checkout(e.product("p2", e.server()), "perm@example.com")
	e.pay(id2)
	e.billing.FailNext("POST", "/accounts", 422, 1)
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`)); err != nil {
		t.Fatalf("permanent failure must not retry: %v", err)
	}
	if o, _ := e.svc.Order(ctx, id2); o.Status != OrderFailed {
		t.Fatalf("order = %s", o.Status)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id2); st != EntitlementFailed {
		t.Fatalf("entitlement = %s", st)
	}
	if auditCount(e, "commerce.entitlement.failed") != 1 {
		t.Fatal("failure not audited")
	}
	// Retrying a failed order is a no-op.
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRefundSuspends(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "refund@example.com")
	e.pay(id)
	e.drain(JobProvision)
	cust := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id)

	body, h := stripeEvent("evt_refund", evChargeRefunded, map[string]any{"id": "ch_1", "object": "charge", "payment_intent": "pi_" + id, "refunded": true})
	if r := e.webhook(body, h); r.StatusCode != 200 {
		t.Fatalf("refund webhook: %d", r.StatusCode)
	}
	e.webhook(body, h) // replay
	if o, _ := e.svc.Order(context.Background(), id); o.Status != OrderRefunded {
		t.Fatalf("order = %s", o.Status)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id); st != EntitlementSuspended {
		t.Fatalf("entitlement = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobSuspend); n != 1 {
		t.Fatalf("suspend jobs = %d", n)
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}
	if a, _ := e.billing.Account(cust); a.Status != "suspended" {
		t.Fatalf("remote account = %+v", a)
	}
	if auditCount(e, "commerce.order.refunded") != 1 || auditCount(e, "commerce.entitlement.suspend") != 1 {
		t.Fatal("refund not audited")
	}

	// A refund that arrives before provisioning: the job sees it and stops.
	id2 := e.checkout(e.product("p2", e.server()), "early-refund@example.com")
	e.pay(id2)
	b2, h2 := stripeEvent("evt_refund2", evChargeRefunded, map[string]any{"id": "ch_2", "payment_intent": "pi_" + id2, "refunded": true})
	e.webhook(b2, h2)
	e.drain(JobProvision)
	if n := e.count(`SELECT count(*) FROM entitlements WHERE order_id = ?`, id2); n != 0 {
		t.Fatalf("refunded order provisioned: %d", n)
	}
	// Partial refunds keep access.
	id3 := e.checkout(e.product("p3", e.server()), "partial@example.com")
	e.pay(id3)
	e.drain(JobProvision)
	b3, h3 := stripeEvent("evt_partial", evChargeRefunded, map[string]any{"id": "ch_3", "payment_intent": "pi_" + id3, "refunded": false})
	e.webhook(b3, h3)
	if o, _ := e.svc.Order(context.Background(), id3); o.Status != OrderActive {
		t.Fatalf("partial refund order = %s", o.Status)
	}
}

func TestExpireCronSuspendsPastEnd(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()
	id := e.checkout(e.product("p", srv), "exp@example.com")
	e.pay(id)
	e.drain(JobProvision)
	cust := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id)
	// A second customer whose access is still valid.
	id2 := e.checkout(e.product("p2", srv), "valid@example.com")
	e.pay(id2)
	e.drain(JobProvision)

	past := db.FormatTime(time.Now().Add(-time.Hour))
	e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = ? WHERE order_id = ?`, past, id)
	if err := e.svc.expire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.expire(ctx); err != nil { // second run: nothing new
		t.Fatal(err)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id); st != EntitlementExpired {
		t.Fatalf("expired entitlement = %s", st)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE order_id = ?`, id2); st != EntitlementActive {
		t.Fatalf("valid entitlement = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobSuspend); n != 1 {
		t.Fatalf("suspend jobs = %d", n)
	}
	e.drain(JobSuspend)
	if a, _ := e.billing.Account(cust); a.Status != "suspended" {
		t.Fatalf("remote account = %+v", a)
	}
	if auditCount(e, "commerce.entitlement.expire") != 1 {
		t.Fatal("expiry not audited")
	}

	// An expired entitlement of a customer who bought again (still active on
	// the same server) does not suspend the shared account.
	id3 := e.checkout(e.product("p3", srv), "valid@example.com")
	e.pay(id3)
	e.drain(JobProvision)
	cust2 := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id2)
	// The second purchase extends from the first period's end.
	first := e.str(`SELECT ends_at FROM entitlements WHERE order_id = ?`, id2)
	if st := e.str(`SELECT starts_at FROM entitlements WHERE order_id = ?`, id3); st != first {
		t.Fatalf("extension starts_at = %s, want %s", st, first)
	}
	e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = ? WHERE order_id = ?`, past, id2)
	e.svc.expire(ctx)
	e.drain(JobSuspend)
	if a, _ := e.billing.Account(cust2); a.Status != "active" {
		t.Fatalf("shared account suspended although another entitlement is active: %+v", a)
	}
}

func TestAccountPageListsEntitlementsAndOpenLink(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "acct@example.com")
	e.pay(id)
	e.drain(JobProvision)
	entID := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id)

	// Anonymous: the sign-in form.
	resp, page := e.do("GET", "/account", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("account: %d", resp.StatusCode)
	}
	mustContain(t, page, `action="/account/login"`, `name="email"`)
	csrf := csrfFieldRe.FindStringSubmatch(page)[1]
	// Requesting a link emails the customer (and says "sent" either way).
	resp, _ = e.do("POST", "/account/login", url.Values{"_csrf": {csrf}, "email": {"acct@example.com"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account?sent=1" {
		t.Fatalf("login request: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = e.do("POST", "/account/login", url.Values{"_csrf": {csrf}, "email": {"unknown@example.com"}})
	if resp.Header.Get("Location") != "/account?sent=1" {
		t.Fatal("unknown email must look the same")
	}
	// Both requests queued a job (same work for known and unknown addresses).
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobMagicMail); n != 2 {
		t.Fatalf("magic mail jobs = %d", n)
	}
	e.drain(JobMagicMail)
	if n := len(e.mail.all()); n != 2 { // access email + one login email
		t.Fatalf("mails = %d", n)
	}
	// Staff accounts never get links from the public form.
	e.users().Create(context.Background(), "staff@example.com", "Staff", testPassword, "editor")
	e.do("POST", "/account/login", url.Values{"_csrf": {csrf}, "email": {"staff@example.com"}})
	e.drain(JobMagicMail)
	if n := len(e.mail.all()); n != 2 {
		t.Fatalf("staff got a link: %d mails", n)
	}

	// Signed in: entitlements with status, end date and Open.
	u, _ := e.users().GetByEmail(context.Background(), "acct@example.com")
	e.signInCustomer(u.ID)
	resp, page = e.do("GET", "/account", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("account: %d", resp.StatusCode)
	}
	ends := e.str(`SELECT ends_at FROM entitlements WHERE id = ?`, entID)
	mustContain(t, page, "Plan p", `data-status="active"`, `datetime="`+ends+`"`, `action="/account/open/`+entID+`"`)
	csrf = csrfFieldRe.FindStringSubmatch(page)[1]
	resp, _ = e.do("POST", "/account/open/"+entID, url.Values{"_csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "https://billing.example.test/login/") {
		t.Fatalf("open: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Without CSRF: refused.
	if r, _ := e.do("POST", "/account/open/"+entID, url.Values{}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("open without csrf: %d", r.StatusCode)
	}
	// Another customer cannot open it.
	id2 := e.checkout(e.product("p2", e.server()), "other@example.com")
	_ = id2
	u2, _ := e.users().GetByEmail(context.Background(), "other@example.com")
	e.c = newClient()
	e.signInCustomer(u2.ID)
	csrf = e.csrfFrom("/product/p")
	if r, _ := e.do("POST", "/account/open/"+entID, url.Values{"_csrf": {csrf}}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign open: %d", r.StatusCode)
	}
}
