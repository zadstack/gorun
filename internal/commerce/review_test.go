package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// TestWebhookSessionMustMatchOrder: a paid session that does not match the
// order exactly (e.g. a cheap Payment Link carrying another order's
// client_reference_id) never pays it.
func TestWebhookSessionMustMatchOrder(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "m@example.com")
	ctx := context.Background()
	cases := map[string]func(m map[string]any){
		"wrong amount":     func(m map[string]any) { m["amount_total"] = 100 },
		"missing amount":   func(m map[string]any) { delete(m, "amount_total") },
		"wrong session id": func(m map[string]any) { m["id"] = "cs_test_payment_link" },
		"wrong currency":   func(m map[string]any) { m["currency"] = "usd" },
		"wrong mode":       func(m map[string]any) { m["mode"] = "subscription" },
		"metadata differs": func(m map[string]any) { m["metadata"] = map[string]any{"order_id": "someone-else"} },
	}
	i := 0
	for name, mutate := range cases {
		i++
		obj := completedSession(id)
		mutate(obj)
		body, h := stripeEvent("evt_bad_"+string(rune('a'+i)), evCheckoutCompleted, obj)
		if r := e.webhook(body, h); r.StatusCode != 200 {
			t.Fatalf("%s: %d", name, r.StatusCode)
		}
		if o, _ := e.svc.Order(ctx, id); o.Status != OrderPending || o.StripeSessionID != "cs_test_"+id {
			t.Fatalf("%s: order = %s session %s", name, o.Status, o.StripeSessionID)
		}
	}
	if n := auditCount(e, "commerce.order.mismatch"); n != len(cases) {
		t.Fatalf("mismatch audits = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM stripe_events WHERE processed_at != ''`); n != len(cases) {
		t.Fatalf("mismatched events must still be recorded as processed: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobProvision); n != 0 {
		t.Fatalf("provision jobs = %d", n)
	}
	// The matching session pays.
	e.pay(id)
	if o, _ := e.svc.Order(ctx, id); o.Status != OrderPaid {
		t.Fatalf("correct session: %s", o.Status)
	}
	// A cancelled order is never paid by a late completion.
	id2 := e.checkout(e.product("p2", e.server()), "m2@example.com")
	b, h := stripeEvent("evt_exp", evCheckoutExpired, map[string]any{"id": "cs_test_" + id2, "client_reference_id": id2})
	e.webhook(b, h)
	if o, _ := e.svc.Order(ctx, id2); o.Status != OrderCancelled {
		t.Fatalf("expired: %s", o.Status)
	}
	e.pay(id2)
	if o, _ := e.svc.Order(ctx, id2); o.Status != OrderCancelled {
		t.Fatalf("cancelled order paid: %s", o.Status)
	}
	if auditCount(e, "commerce.order.cancelled") != 1 {
		t.Fatal("expiry not audited")
	}
}

func TestCheckoutAsyncFailureAndRefundBeforeCompletion(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()
	id := e.checkout(e.product("p", srv), "a@example.com")
	b, h := stripeEvent("evt_fail", evCheckoutAsyncFailed, map[string]any{"id": "cs_test_" + id, "client_reference_id": id})
	e.webhook(b, h)
	if o, _ := e.svc.Order(ctx, id); o.Status != OrderFailed || auditCount(e, "commerce.order.payment_failed") != 1 {
		t.Fatalf("async failure: %s", o.Status)
	}
	// The refund arrives before the completion (by payment intent only).
	id2 := e.checkout(e.product("p2", srv), "b@example.com")
	b, h = stripeEvent("evt_ref_early", evChargeRefunded, map[string]any{"id": "ch_1", "payment_intent": "pi_" + id2, "refunded": true})
	e.webhook(b, h)
	e.pay(id2)
	if o, _ := e.svc.Order(ctx, id2); o.Status != OrderRefunded {
		t.Fatalf("refund-first order = %s", o.Status)
	}
	// Or names the order in its metadata: refunded_pending is set.
	id3 := e.checkout(e.product("p3", srv), "c@example.com")
	b, h = stripeEvent("evt_ref_meta", evChargeRefunded, map[string]any{"id": "ch_2", "payment_intent": "pi_other", "refunded": true,
		"metadata": map[string]any{"order_id": id3}})
	e.webhook(b, h)
	if n := e.count(`SELECT refunded_pending FROM orders WHERE id = ?`, id3); n != 1 {
		t.Fatal("refunded_pending not set")
	}
	e.pay(id3)
	if o, _ := e.svc.Order(ctx, id3); o.Status != OrderRefunded {
		t.Fatalf("flagged order = %s", o.Status)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobProvision); n != 0 {
		t.Fatalf("refunded orders provisioned: %d jobs", n)
	}
}

func TestConcurrentWebhookDeliveries(t *testing.T) {
	e := boot(t)
	id := e.checkout(e.product("p", e.server()), "par@example.com")
	body, h := stripeEvent("evt_parallel", evCheckoutCompleted, completedSession(id))
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", e.srv.URL+"/webhooks/stripe", strings.NewReader(string(body)))
			req.Header.Set("Stripe-Signature", h)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				codes[i] = resp.StatusCode
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("delivery %d: %d", i, c)
		}
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ?`, JobProvision); n != 1 {
		t.Fatalf("provision jobs = %d", n)
	}
	// Two provision runs in parallel still give one entitlement and one create.
	payload := json.RawMessage(`{"order_id":"` + id + `"}`)
	var pw sync.WaitGroup
	for range 2 {
		pw.Add(1)
		go func() { defer pw.Done(); _ = e.svc.provision(context.Background(), payload) }()
	}
	pw.Wait()
	_ = e.svc.provision(context.Background(), payload)
	if n := e.count(`SELECT count(*) FROM entitlements WHERE order_id = ?`, id); n != 1 {
		t.Fatalf("entitlements = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM provisioning_ops WHERE entitlement_id IN (SELECT id FROM entitlements WHERE order_id = ?) AND kind = 'create'`, id); n != 1 {
		t.Fatalf("create ops = %d", n)
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id); st != OrderActive {
		t.Fatalf("order = %s", st)
	}
}

func TestProvisionLocalErrorsRetryAndExhaust(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()

	// A local failure (undecryptable server secret) is retried, not failed.
	id := e.checkout(e.product("p", srv), "local@example.com")
	e.pay(id)
	good := e.str(`SELECT secret_enc FROM billing_servers WHERE id = ?`, srv.ID)
	e.app.Deps.DB.Exec(`UPDATE billing_servers SET secret_enc = 'garbage' WHERE id = ?`, srv.ID)
	payload := json.RawMessage(`{"order_id":"` + id + `","job_key":"commerce.provision:` + id + `"}`)
	if err := e.svc.provision(ctx, payload); err == nil {
		t.Fatal("decrypt failure must return an error for a retry")
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id); st != OrderProvisioning {
		t.Fatalf("order after local error = %s", st)
	}
	e.app.Deps.DB.Exec(`UPDATE billing_servers SET secret_enc = ? WHERE id = ?`, good, srv.ID)

	// A transient database error (write lock held: SQLITE_BUSY) is retried.
	id2 := e.checkout(e.product("p2", srv), "busy@example.com")
	e.pay(id2)
	locked, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = db.InTx(ctx, e.app.Deps.DB, func(tx *sql.Tx) error {
			_, _ = tx.Exec(`UPDATE settings SET updated_at = updated_at`)
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`))
	close(release)
	if err == nil {
		t.Fatal("busy database must return an error for a retry")
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id2); st == OrderFailed {
		t.Fatal("transient database error failed the order")
	}
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`)); err != nil {
		t.Fatal(err)
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id2); st != OrderActive {
		t.Fatalf("after retry = %s", st)
	}

	// Exhaustion: on the job's last attempt a transient failure fails the order.
	e.billing.FailNext("POST", "/accounts", 503, 10)
	// The worker runs the final attempt with attempt == max in ctx.
	if err := e.svc.provision(jobs.ContextWithAttempt(ctx, provisionMaxAttempts, provisionMaxAttempts), payload); err != nil {
		t.Fatalf("last attempt must not ask for another retry: %v", err)
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id); st != OrderFailed {
		t.Fatalf("exhausted order = %s", st)
	}
	if auditCount(e, "commerce.order.provision_failed") != 1 {
		t.Fatal("exhaustion not audited")
	}
	// The worker records that last attempt as finished (it returned nil).
	e.app.Deps.DB.Exec(`UPDATE jobs SET status = 'done' WHERE kind = ?`, JobProvision)
	if st := e.str(`SELECT status FROM provisioning_ops WHERE idempotency_key = ?`, "create:"+id); st != "failed" {
		t.Fatalf("op = %s", st)
	}

	// Admin "Retry provisioning" re-enqueues with a fresh op key.
	e.signInAdmin("admin@example.com", "admin")
	_, page := e.do("GET", "/admin/commerce/orders/"+id, nil)
	mustContain(t, page, "/admin/commerce/orders/"+id+"/retry")
	e.billing.ClearFailures()
	if r, _ := e.adminPost("/admin/commerce/orders/"+id+"/retry", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("retry: %d", r.StatusCode)
	}
	if errs := e.drain(JobProvision); len(errs) > 0 {
		t.Fatal(errs)
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id); st != OrderActive {
		t.Fatalf("after admin retry = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM provisioning_ops WHERE idempotency_key = ?`, "create:"+id+":retry1"); n != 1 {
		t.Fatalf("retry op = %d", n)
	}
	if auditCount(e, "commerce.order.retry") != 1 {
		t.Fatal("retry not audited")
	}
	// Active orders cannot be retried.
	if err := e.svc.RetryProvisioning(ctx, id); err == nil {
		t.Fatal("retry of an active order accepted")
	}
}

func TestBuyAgainWhilePendingExtends(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()
	id1 := e.checkout(e.product("p", srv), "twice@example.com")
	id2 := e.checkout(e.product("p2", srv), "twice@example.com")
	e.pay(id1)
	e.pay(id2)
	// The first provisioning fails transiently: its entitlement stays pending.
	e.billing.FailNext("POST", "/accounts", 503, 1)
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id1+`"}`)); err == nil {
		t.Fatal("want a retryable error")
	}
	first := e.str(`SELECT ends_at FROM entitlements WHERE order_id = ?`, id1)
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`)); err != nil {
		t.Fatal(err)
	}
	if st := e.str(`SELECT starts_at FROM entitlements WHERE order_id = ?`, id2); st != first {
		t.Fatalf("second period starts %s, want after the pending first (%s)", st, first)
	}
}

func TestPeriodEndParsingAndFallback(t *testing.T) {
	want := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, v := range []any{"2026-11-10T12:00:00Z", "2026-11-10T16:00:00+04:00", "2026-11-10T16:00:00+0400", "2026-11-10 12:00:00",
		"2026-11-10T12:00:00", float64(want.Unix()), "1794312000"} {
		got, ok := parsePeriodEnd(v)
		if !ok || !got.Equal(want) {
			t.Errorf("parsePeriodEnd(%v) = %s %v", v, got, ok)
		}
	}
	if got, ok := parsePeriodEnd("2026-11-10"); !ok || !got.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date only = %s", got)
	}
	// Epoch seconds must look like a real timestamp (at least 9 digits,
	// i.e. 1973 onwards, and before year 10000): "12" or 3600 are not dates.
	for _, v := range []any{"soon", "", nil, float64(-1), float64(12), "3600", json.Number("99999999"), float64(1e13), "253402300800"} {
		if _, ok := parsePeriodEnd(v); ok {
			t.Errorf("parsePeriodEnd(%v) accepted", v)
		}
	}

	e := boot(t)
	ctx := context.Background()
	// Unparseable period_end from the server: keep the one we sent.
	e.billing.SetPeriodEndAnswer("next month-ish")
	id := e.checkout(e.product("p", e.server()), "pe@example.com")
	e.pay(id)
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	ent, _, _ := e.svc.Entitlements(ctx, ListQuery{})
	if ent[0].Status != EntitlementActive || ent[0].EndsAt != e.str(`SELECT ends_at FROM entitlements WHERE order_id = ?`, id) {
		t.Fatalf("fallback entitlement = %+v", ent[0])
	}
	var sentBody map[string]any
	for _, c := range e.billing.Calls() {
		if c.Path == "/accounts" {
			sentBody = c.Body
		}
	}
	if got, _ := parsePeriodEnd(sentBody["period_end"]); db.FormatTime(got) != ent[0].EndsAt {
		t.Fatalf("ends_at %s, sent %v", ent[0].EndsAt, sentBody["period_end"])
	}
	// Epoch seconds from the server are honoured.
	e.billing.SetPeriodEndAnswer(float64(want.Unix()))
	id2 := e.checkout(e.product("p2", e.server()), "pe2@example.com")
	e.pay(id2)
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id2+`"}`)); err != nil {
		t.Fatal(err)
	}
	if got := e.str(`SELECT ends_at FROM entitlements WHERE order_id = ?`, id2); got != db.FormatTime(want) {
		t.Fatalf("epoch ends_at = %s", got)
	}
}

func TestCallOpDoneShortCircuitAndAdminSuspendSharedAccount(t *testing.T) {
	e := boot(t)
	srv := e.server()
	ctx := context.Background()
	id1 := e.checkout(e.product("p", srv), "share@example.com")
	e.pay(id1)
	e.drain(JobProvision)
	ent1 := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id1)
	cust := e.str(`SELECT customer_id FROM orders WHERE id = ?`, id1)

	// A done op answers from storage: the server is not called again.
	before := len(e.billing.Calls())
	res, err := e.svc.callOp(ctx, ent1, "create", "create:"+id1, "POST", "/accounts", map[string]string{}, false, false)
	if err != nil || len(res.Data) == 0 || len(e.billing.Calls()) != before {
		t.Fatalf("done op re-sent: %v calls %d→%d", err, before, len(e.billing.Calls()))
	}

	// With a second active entitlement on the same server, an admin
	// suspension of the first keeps the shared remote account active.
	id2 := e.checkout(e.product("p2", srv), "share@example.com")
	e.pay(id2)
	e.drain(JobProvision)
	if err := e.svc.Suspend(ctx, ent1); err != nil {
		t.Fatal(err)
	}
	if st := e.str(`SELECT status FROM entitlements WHERE id = ?`, ent1); st != EntitlementSuspended {
		t.Fatalf("local = %s", st)
	}
	if a, _ := e.billing.Account(cust); a.Status != "active" {
		t.Fatalf("shared account suspended: %+v", a)
	}
	// Suspend/resume answers are validated like PHP valid_result.
	if expectStatus(json.RawMessage(`{"status":"active"}`), "suspended", "inactive") == nil ||
		expectStatus(json.RawMessage(`{"status":"inactive"}`), "suspended", "inactive") != nil ||
		expectStatus(json.RawMessage(`[]`), "active") == nil {
		t.Fatal("expectStatus")
	}
	// Login links require an unexpired period.
	ent2 := e.str(`SELECT id FROM entitlements WHERE order_id = ?`, id2)
	e.app.Deps.DB.Exec(`UPDATE entitlements SET ends_at = '2020-01-01T00:00:00Z' WHERE id = ?`, ent2)
	if _, err := e.svc.LoginLink(ctx, ent2); err != ErrNotActive {
		t.Fatalf("login link past period: %v", err)
	}
}

func TestAdminPostWithoutCSRFAndPriceRules(t *testing.T) {
	e := boot(t)
	e.signInAdmin("admin@example.com", "admin")
	if r, _ := e.do("POST", "/admin/commerce/products", url.Values{"name": {"X"}, "price": {"1"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("admin POST without _csrf: %d", r.StatusCode)
	}
	if r, _ := e.do("POST", "/admin/commerce/settings", url.Values{"currency": {"USD"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("settings POST without _csrf: %d", r.StatusCode)
	}
	for _, bad := range []string{"1e3", "0x10", "+5", " 5 5", "1.", ".5", "1,2.3"} {
		if _, err := ParsePrice(bad, "AED"); err == nil {
			t.Errorf("ParsePrice(%q) accepted", bad)
		}
	}
	// Blank currency → store currency (KWD: 3 decimals) for the scale too.
	e.app.Deps.Settings.Set(context.Background(), SettingCurrency, "KWD")
	if r, _ := e.adminPost("/admin/commerce/products", url.Values{"name": {"Kuwait"}, "price": {"1.250"}, "currency": {""}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("blank currency: %d", r.StatusCode)
	}
	p, err := e.svc.Product(context.Background(), "kuwait")
	if err != nil || p.Currency != "KWD" || p.PriceMinor != 1250 {
		t.Fatalf("product = %+v %v", p, err)
	}
}

// A matching paid completion for an order that is no longer pending (e.g.
// cancelled) is money taken for a closed order: it is audited as
// commerce.order.paid_closed for an admin, and nothing is provisioned. A
// duplicate completion of the same session for a paid order is not.
func TestPaidCompletionForClosedOrderIsAudited(t *testing.T) {
	e := boot(t)
	srv := e.server()
	closed := e.checkout(e.product("p", srv), "closed@example.com")
	e.app.Deps.DB.Exec(`UPDATE orders SET status = 'cancelled' WHERE id = ?`, closed)
	e.pay(closed)
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, closed); st != OrderCancelled {
		t.Fatalf("closed order became %s", st)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.order.paid_closed' AND target_id = ?
		AND json_extract(meta_json, '$.status') = 'cancelled' AND json_extract(meta_json, '$.session') = ?`, closed, "cs_test_"+closed); n != 1 {
		t.Fatalf("paid_closed audit rows: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND payload_json LIKE ?`, JobProvision, "%"+closed+"%"); n != 0 {
		t.Fatal("a closed order must not be provisioned")
	}

	// A mismatching session (e.g. a cheap Payment Link carrying the closed
	// order's reference) is a mismatch, never a paid_closed to refund/fulfil.
	closed2 := e.checkout(e.product("p3", srv), "closed2@example.com")
	e.app.Deps.DB.Exec(`UPDATE orders SET status = 'cancelled' WHERE id = ?`, closed2)
	cheap := completedSession(closed2)
	cheap["amount_total"] = 100
	body, h := stripeEvent("evt_cheap_"+closed2, evCheckoutCompleted, cheap)
	if r := e.webhook(body, h); r.StatusCode != 200 {
		t.Fatalf("cheap: %d", r.StatusCode)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.order.paid_closed' AND target_id = ?`, closed2); n != 0 {
		t.Fatal("a mismatching session must not be reported as paid_closed")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.order.mismatch' AND target_id = ?`, closed2); n != 1 {
		t.Fatalf("mismatch rows: %d", n)
	}

	paid := e.checkout(e.product("p2", srv), "paid@example.com")
	e.pay(paid)
	body, h = stripeEvent("evt_dup_"+paid, evCheckoutCompleted, completedSession(paid)) // Stripe redelivery under a new event id
	if r := e.webhook(body, h); r.StatusCode != 200 {
		t.Fatalf("duplicate: %d", r.StatusCode)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.order.paid_closed' AND target_id = ?`, paid); n != 0 {
		t.Fatal("a duplicate completion of the paying session is not a closed-order payment")
	}
}

// RetryProvisioning gives the customer the full access window from the
// retry, not from the first (failed) attempt.
func TestRetryProvisioningRecomputesWindowFromNow(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	id := e.checkout(e.product("p", e.server()), "late@example.com")
	e.pay(id)
	e.billing.FailNext("POST", "/accounts", 400, 1) // permanent: fails the order at once
	if err := e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	if st := e.str(`SELECT status FROM orders WHERE id = ?`, id); st != OrderFailed {
		t.Fatalf("order = %s", st)
	}
	later := time.Now().UTC().Add(10 * 24 * time.Hour).Truncate(time.Second)
	e.svc.now = func() time.Time { return later }
	if err := e.svc.RetryProvisioning(ctx, id); err != nil {
		t.Fatal(err)
	}
	starts := e.str(`SELECT starts_at FROM entitlements WHERE order_id = ?`, id)
	ends := e.str(`SELECT ends_at FROM entitlements WHERE order_id = ?`, id)
	if starts != db.FormatTime(later) || ends != db.FormatTime(addMonths(later, 1)) {
		t.Fatalf("window after retry = %s → %s, want from %s", starts, ends, db.FormatTime(later))
	}
}
