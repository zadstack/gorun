package commerce

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

// fakeVerifier stands in for the read-only Stripe key check.
type fakeVerifier struct {
	livemode bool
	err      error
	keys     []string
}

func (f *fakeVerifier) Verify(_ context.Context, key string) (keyVerification, error) {
	f.keys = append(f.keys, key)
	if f.err != nil {
		return keyVerification{}, f.err
	}
	return keyVerification{Livemode: f.livemode}, nil
}

func (f *fakeVerifier) lastKey(t *testing.T) string {
	t.Helper()
	if len(f.keys) == 0 {
		t.Fatal("verifier was not called")
	}
	return f.keys[len(f.keys)-1]
}

// modeWebhook posts a signed event to /webhooks/stripe/<mode>.
func (e *env) modeWebhook(mode, secret string, body []byte) int {
	e.t.Helper()
	sp := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: secret, Timestamp: time.Now()})
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/webhooks/stripe/"+mode, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", sp.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestStripeModeBootstrapNewStoreStartsInTest(t *testing.T) {
	e := bootCfg(t, func(c *config.Config) { c.Stripe.SecretKey, c.Stripe.WebhookSecret = "", "" })
	ctx := context.Background()
	if got := e.svc.stripeMode(ctx); got != "test" {
		t.Fatalf("new store mode = %s, want test", got)
	}
	if got := e.svc.d.Settings.GetString(ctx, SettingStripeMode, ""); got != "test" {
		t.Fatalf("mode must be stored explicitly, got %q", got)
	}
	if got := e.svc.d.Settings.GetString(ctx, SettingSettingsVersion, ""); got != stripeModeSettingsVersion {
		t.Fatalf("settings version = %q", got)
	}
	if auditCount(e, "commerce.settings.bootstrap") != 1 {
		t.Fatal("bootstrap not audited")
	}
	// A later boot of the same store is a no-op even after an admin chose live.
	if err := e.svc.d.Settings.Set(ctx, SettingStripeMode, "live"); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapStripeMode(ctx, e.svc); err != nil {
		t.Fatal(err)
	}
	if e.svc.stripeMode(ctx) != "live" || auditCount(e, "commerce.settings.bootstrap") != 1 {
		t.Fatal("bootstrap ran twice")
	}
}

func TestStripeModeBootstrapExistingStoreKeepsLive(t *testing.T) {
	e := boot(t) // fresh database, but the environment configures the live slot
	ctx := context.Background()
	if got := e.svc.d.Settings.GetString(ctx, SettingStripeMode, ""); got != "live" {
		t.Fatalf("fresh install with STRIPE_* in the environment must be pinned live, got %q", got)
	}
	if epoch, err := kernel.InstalledEpoch(ctx, e.svc.db); err != nil || epoch != kernel.InstallEpoch {
		t.Fatalf("install epoch = %d, %v", epoch, err)
	}

	// Upgrade fixture: a database created before the install stamp existed
	// (no kernel.install_epoch), a populated CMS (admin user, product,
	// settings), no orders and no Stripe secret anywhere. Its effective mode
	// is live (absent) and the bootstrap must pin exactly that.
	e.signInAdmin("admin@example.com", "admin")
	e.product("catalog-only", e.server())
	for _, k := range []string{SettingStripeMode, SettingSettingsVersion, kernel.InstallSettingKey} {
		if err := e.svc.d.Settings.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.d.Config.Stripe.SecretKey, e.svc.d.Config.Stripe.WebhookSecret = "", ""
	if got := e.svc.stripeMode(ctx); got != "live" {
		t.Fatalf("absent mode must still mean live, got %q", got)
	}
	if e.count(`SELECT count(*) FROM orders`) != 0 || e.count(`SELECT count(*) FROM users`) == 0 {
		t.Fatal("fixture: want users and no orders")
	}
	if err := bootstrapStripeMode(ctx, e.svc); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.d.Settings.GetString(ctx, SettingStripeMode, ""); got != "live" {
		t.Fatalf("upgraded store without commerce activity bootstrapped to %q, want live", got)
	}
	// Two bootstraps so far (the boot with STRIPE_* in the environment, and
	// this upgrade), both pinned live for an existing store.
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.settings.bootstrap' AND meta_json LIKE '%existing_or_upgraded_store%'`) != 2 {
		t.Fatal("upgrade bootstrap not audited as existing")
	}

	// An upgraded store with orders is existing for the same reason.
	for _, k := range []string{SettingStripeMode, SettingSettingsVersion} {
		if err := e.svc.d.Settings.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.d.Config.Stripe.SecretKey = "sk_test_config"
	e.checkout(e.product("history", e.server()), "old@example.com")
	e.svc.d.Config.Stripe.SecretKey = ""
	if err := bootstrapStripeMode(ctx, e.svc); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.d.Settings.GetString(ctx, SettingStripeMode, ""); got != "live" {
		t.Fatalf("store with orders bootstrapped to %q, want live", got)
	}
}

func TestStripeModeSwitchToLiveRequiresConfirmationAndUsableKeys(t *testing.T) {
	e := boot(t)
	fv := &fakeVerifier{livemode: true}
	e.svc.verifier = fv
	ctx := context.Background()
	e.signInAdmin("admin@example.com", "admin")

	// Switching to test needs no confirmation; a live-prefixed key in the test slot is refused.
	if r, page := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"test"}, "stripe_test_secret_key": {"sk_live_in_test_slot"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live key in test slot: %d\n%s", r.StatusCode, page)
	}
	if e.svc.stripeMode(ctx) != "live" || e.svc.stripeSecretFor(ctx, "test") != "" {
		t.Fatal("refused switch must save nothing")
	}
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"test"}, "stripe_test_secret_key": {"sk_test_only"}, "stripe_test_webhook_secret": {"whsec_test_only"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch to test: %d", r.StatusCode)
	}
	if e.svc.stripeMode(ctx) != "test" {
		t.Fatal("mode not test")
	}
	if len(fv.keys) != 0 {
		t.Fatal("switching to test must not call Stripe")
	}

	// 1. no confirmation
	if r, page := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live without confirmation: %d\n%s", r.StatusCode, page)
	} else {
		mustContain(t, page, `id="c-mode"`, "field-error")
	}
	// 2. confirmed, but the live slot holds a test-prefixed key (the env value sk_test_config)
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live with test-prefixed key: %d", r.StatusCode)
	}
	// 3. confirmed, live key present, but no live webhook secret
	e.svc.d.Config.Stripe.WebhookSecret = ""
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}, "stripe_secret_key": {"sk_live_abc"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live without webhook secret: %d", r.StatusCode)
	}
	if e.svc.stripeSecretFor(ctx, "live") != "sk_test_config" {
		t.Fatal("refused switch must not store the submitted key")
	}
	e.svc.d.Config.Stripe.WebhookSecret = testWebhookSecret
	// 4. Stripe rejects the key
	fv.err = errors.New("invalid api key")
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}, "stripe_secret_key": {"sk_live_abc"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live with rejected key: %d", r.StatusCode)
	}
	// 5. authenticates, but Stripe says it is not a live key
	fv.err, fv.livemode = nil, false
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}, "stripe_secret_key": {"sk_live_abc"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("live with livemode=false key: %d", r.StatusCode)
	}
	if e.svc.stripeMode(ctx) != "test" || auditCount(e, "commerce.settings.mode") != 1 {
		t.Fatalf("mode = %s, mode audits = %d (only the test switch)", e.svc.stripeMode(ctx), auditCount(e, "commerce.settings.mode"))
	}
	// 6. everything consistent
	fv.livemode = true
	if r, page := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}, "stripe_secret_key": {"sk_live_abc"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("live switch: %d\n%s", r.StatusCode, page)
	}
	if e.svc.stripeMode(ctx) != "live" || e.svc.stripeSecretFor(ctx, "live") != "sk_live_abc" || fv.lastKey(t) != "sk_live_abc" {
		t.Fatal("live switch not applied with the submitted key")
	}
	if auditCount(e, "commerce.settings.mode") != 2 {
		t.Fatal("live switch not audited")
	}
	st := e.svc.stripeStatus(ctx, "live")
	if st.Auth.State != authAuthenticated {
		t.Fatalf("after a verified switch auth = %+v", st.Auth)
	}
	// Saving the form again in live mode is not a switch: no confirmation, no Stripe call.
	calls := len(fv.keys)
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}}); r.StatusCode != http.StatusSeeOther || len(fv.keys) != calls {
		t.Fatalf("resave in live: %d, calls %d→%d", r.StatusCode, calls, len(fv.keys))
	}
	_, page := e.do("GET", "/admin/commerce/settings", nil)
	mustContain(t, page, `data-stripe-mode="live"`, `name="confirm_live"`)
}

func TestStripeVerifyKeyAndThreeStates(t *testing.T) {
	e := boot(t)
	fv := &fakeVerifier{livemode: false}
	e.svc.verifier = fv
	ctx := context.Background()
	e.signInAdmin("admin@example.com", "admin")

	st := e.svc.stripeStatus(ctx, "live")
	if !st.KeyConfigured || st.KeySource != "env" || st.KeyPrefix != "test" || st.Auth.State != authUnverified || st.Delivery.Verified {
		t.Fatalf("initial live status = %+v", st)
	}
	if st := e.svc.stripeStatus(ctx, "test"); st.KeyConfigured || st.WebhookConfigured || st.Auth.State != authUnverified {
		t.Fatalf("initial test status = %+v", st)
	}
	// Verifying a mode without a key never calls Stripe.
	if r, _ := e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"test"}}); r.StatusCode != http.StatusSeeOther || len(fv.keys) != 0 {
		t.Fatalf("verify without key: %d, calls %d", r.StatusCode, len(fv.keys))
	}
	if r, _ := e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"sandbox"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("verify invalid mode: %d", r.StatusCode)
	}
	// A key that authenticates as test while sitting in the live slot is a mismatch, not authenticated.
	if r, _ := e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"live"}}); r.StatusCode != http.StatusSeeOther || fv.lastKey(t) != "sk_test_config" {
		t.Fatalf("verify live: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "live"); st.Auth.State != authMismatch || st.Auth.CheckedAt == "" {
		t.Fatalf("mismatch status = %+v", st.Auth)
	}
	fv.livemode = true
	e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"live"}})
	if st := e.svc.stripeStatus(ctx, "live"); st.Auth.State != authAuthenticated {
		t.Fatalf("authenticated status = %+v", st.Auth)
	}
	// The result belongs to the key that was checked: a new key is unverified again.
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_secret_key": {"sk_live_new"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("save key: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "live"); st.Auth.State != authUnverified || st.KeySource != "settings" || st.KeyPrefix != "live" {
		t.Fatalf("after key change = %+v", st)
	}
	fv.err = errors.New("401 invalid api key")
	e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"live"}})
	if st := e.svc.stripeStatus(ctx, "live"); st.Auth.State != authFailed || !strings.Contains(st.Auth.Error, "invalid api key") {
		t.Fatalf("failed status = %+v", st.Auth)
	}
	_, page := e.do("GET", "/admin/commerce/settings", nil)
	mustContain(t, page, `data-stripe-auth="failed"`)
	mustNotContain(t, page, "sk_live_new", "sk_test_config")

	// Webhook delivery: the locally signed test event never proves delivery;
	// a signed delivery on the public test endpoint does.
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"test"}, "stripe_test_secret_key": {"sk_test_only"}, "stripe_test_webhook_secret": {"whsec_test_only"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch to test: %d", r.StatusCode)
	}
	if r, _ := e.adminPost("/admin/commerce/settings/test-event", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("test event: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "test"); !st.WebhookConfigured || st.Delivery.Verified {
		t.Fatalf("local test event must not verify delivery: %+v", st.Delivery)
	}
	body, _ := stripeEvent("evt_delivery_1", evSubUpdated, map[string]any{"id": "sub_d", "customer": "cus_d", "status": "active"})
	if code := e.modeWebhook("test", "whsec_test_only", body); code != http.StatusOK {
		t.Fatalf("test endpoint: %d", code)
	}
	st = e.svc.stripeStatus(ctx, "test")
	if !st.Delivery.Verified || st.Delivery.LastAt == "" {
		t.Fatalf("delivery after a signed event = %+v", st.Delivery)
	}
	if st := e.svc.stripeStatus(ctx, "live"); st.Delivery.Verified {
		t.Fatal("a test delivery must not verify the live endpoint")
	}
	// Evidence is bound to the credentials in place when it was verified:
	// rotating the webhook secret or pointing the slot at another account's
	// key starts again from unverified, and the old event rows stay as they
	// are. A row from before revisions were recorded never counts.
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_test_webhook_secret": {"whsec_test_rotated"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("rotate webhook secret: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "test"); st.Delivery.Verified {
		t.Fatalf("delivery still verified after rotating the webhook secret: %+v", st.Delivery)
	}
	if e.count(`SELECT count(*) FROM stripe_events WHERE id = 'evt_delivery_1' AND mode = 'test' AND source = 'stripe'`) != 1 {
		t.Fatal("historic event row was modified")
	}
	body, _ = stripeEvent("evt_delivery_2", evSubUpdated, map[string]any{"id": "sub_d", "customer": "cus_d", "status": "active"})
	if code := e.modeWebhook("test", "whsec_test_rotated", body); code != http.StatusOK {
		t.Fatalf("test endpoint after rotation: %d", code)
	}
	if st := e.svc.stripeStatus(ctx, "test"); !st.Delivery.Verified || st.Delivery.Count != 1 {
		t.Fatalf("delivery after a new signed event = %+v", st.Delivery)
	}
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_test_secret_key": {"sk_test_other_account"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("change key: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "test"); st.Delivery.Verified {
		t.Fatalf("delivery still verified after changing the account key: %+v", st.Delivery)
	}
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_test_secret_key": {"sk_test_only"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("restore key: %d", r.StatusCode)
	}
	if st := e.svc.stripeStatus(ctx, "test"); !st.Delivery.Verified {
		t.Fatal("evidence for the restored configuration must count again")
	}
	if _, err := e.svc.db.ExecContext(ctx, `INSERT INTO stripe_events (id, type, received_at, payload_json, mode, source, config_rev) VALUES ('evt_pre_rev', 'invoice.paid', '2026-01-01T00:00:00Z', '{}', 'live', 'stripe', '')`); err != nil {
		t.Fatal(err)
	}
	if st := e.svc.stripeStatus(ctx, "live"); st.Delivery.Verified {
		t.Fatal("an event without a recorded revision must not verify delivery")
	}
	_, page = e.do("GET", "/admin/commerce/settings", nil)
	mustContain(t, page, `data-stripe-mode="test"`, `data-stripe-delivery="verified"`)

	// Capability report: three honest states.
	reason := func() string {
		var list []admin.ModuleRequirement
		out, err := e.svc.requirements(ctx, &list)
		if err != nil {
			t.Fatal(err)
		}
		return (*out.(*[]admin.ModuleRequirement))[0].Reason
	}
	if got := reason(); got != "configured_not_authenticated" {
		t.Fatalf("reason in test mode = %s", got)
	}
	fv.err = nil
	fv.livemode = false
	e.adminPost("/admin/commerce/settings/verify", url.Values{"mode": {"test"}})
	if got := reason(); got != "verified" {
		t.Fatalf("reason after auth + delivery = %s", got)
	}
}

func TestOrderStripeModeProvenance(t *testing.T) {
	e := boot(t)
	refunds := &fakeRefunds{}
	e.svc.refunds = refunds
	ctx := context.Background()
	srv := e.server()
	p := e.product("prov", srv)

	liveOrder := e.checkout(p, "live@example.com")
	if got := e.str(`SELECT stripe_mode FROM orders WHERE id = ?`, liveOrder); got != "live" {
		t.Fatalf("order created in live mode has stripe_mode %q", got)
	}
	// A pre-provenance order: no mode recorded, and nothing ever assigns one.
	if _, err := e.svc.db.ExecContext(ctx, `UPDATE orders SET stripe_mode = '' WHERE id = ?`, liveOrder); err != nil {
		t.Fatal(err)
	}
	historic := liveOrder

	e.signInAdmin("admin@example.com", "admin")
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"test"}, "stripe_test_secret_key": {"sk_test_only"}, "stripe_test_webhook_secret": {"whsec_test_only"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch to test: %d", r.StatusCode)
	}
	testOrder := e.checkout(p, "test@example.com")
	if got := e.str(`SELECT stripe_mode FROM orders WHERE id = ?`, testOrder); got != "test" {
		t.Fatalf("order created in test mode has stripe_mode %q", got)
	}
	if e.stripe.keys[len(e.stripe.keys)-1] != "sk_test_only" {
		t.Fatal("test order must use the test key")
	}
	if got := e.str(`SELECT stripe_mode FROM orders WHERE id = ?`, historic); got != "" {
		t.Fatalf("historic order relabelled to %q", got)
	}

	// The live endpoint can never pay a test order: audited mismatch, still pending.
	body, _ := stripeEvent("evt_wrong_mode", evCheckoutCompleted, completedSession(testOrder))
	if code := e.modeWebhook("live", testWebhookSecret, body); code != http.StatusOK {
		t.Fatalf("live endpoint: %d", code)
	}
	if got := e.str(`SELECT status FROM orders WHERE id = ?`, testOrder); got != OrderPending {
		t.Fatalf("test order paid by a live event: %s", got)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'commerce.order.mismatch' AND meta_json LIKE '%stripe_mode%'`) != 1 {
		t.Fatal("mode mismatch not audited")
	}
	body, _ = stripeEvent("evt_right_mode", evCheckoutCompleted, completedSession(testOrder))
	if code := e.modeWebhook("test", "whsec_test_only", body); code != http.StatusOK {
		t.Fatalf("test endpoint: %d", code)
	}
	if got := e.str(`SELECT status FROM orders WHERE id = ?`, testOrder); got != OrderPaid {
		t.Fatalf("test order after the test event: %s", got)
	}
	if got := e.str(`SELECT mode || '/' || source FROM stripe_events WHERE id = 'evt_right_mode'`); got != "test/stripe" {
		t.Fatalf("event provenance = %s", got)
	}
	// The historic order is paid through the active (test) secret on the
	// original path and keeps its empty provenance.
	body, _ = stripeEvent("evt_historic", evCheckoutCompleted, completedSession(historic))
	if code := e.modeWebhook("test", "whsec_test_only", body); code != http.StatusOK {
		t.Fatalf("historic pay: %d", code)
	}
	if got := e.str(`SELECT status || '/' || stripe_mode FROM orders WHERE id = ?`, historic); got != "paid/" {
		t.Fatalf("historic order = %s", got)
	}

	// Switching back to live changes nothing on stored orders, and a refund
	// of a test order uses the test key even in live mode.
	fv := &fakeVerifier{livemode: true}
	e.svc.verifier = fv
	if r, _ := e.adminPost("/admin/commerce/settings", url.Values{"currency": {"AED"}, "stripe_mode": {"live"}, "confirm_live": {"1"}, "stripe_secret_key": {"sk_live_real"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch to live: %d", r.StatusCode)
	}
	if got := e.str(`SELECT stripe_mode FROM orders WHERE id = ?`, testOrder); got != "test" {
		t.Fatalf("test order relabelled to %q after switching to live", got)
	}
	if r, _ := e.adminPost("/admin/commerce/orders/"+testOrder+"/refund", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("refund: %d", r.StatusCode)
	}
	if refunds.key != "sk_test_only" {
		t.Fatalf("refund of a test order used key %q", refunds.key)
	}

	_, page := e.do("GET", "/admin/commerce/orders", nil)
	mustContain(t, page, `data-order-mode="test"`, `data-order-mode=""`)
	_, page = e.do("GET", "/admin/commerce/orders/"+testOrder, nil)
	mustContain(t, page, `data-order-mode="test"`)
	o, err := e.svc.Order(ctx, testOrder)
	if err != nil || o.StripeMode != "test" {
		t.Fatalf("Order.StripeMode = %q (%v)", o.StripeMode, err)
	}
}
