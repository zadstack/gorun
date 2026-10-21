package commerce

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

type settingsPage struct {
	Currency                      string
	Mode                          string
	GraceDays                     int
	AutomaticTax                  bool
	RegistrationEnabled           bool
	StripeSet, WebhookSet         bool
	TestKeySet, TestWebhookSet    bool
	StripeFromEnv, WebhookFromEnv bool
	WebhookURL                    string
	TestWebhookURL                string
	LiveWebhookURL                string
	Fields                        map[string]string
}

func (h *adminHandlers) settingsData(r *http.Request) settingsPage {
	ctx := r.Context()
	s := h.svc
	base := s.baseURL(ctx)
	return settingsPage{
		Currency: s.currency(ctx), Mode: s.stripeMode(ctx), GraceDays: s.graceDays(ctx), AutomaticTax: s.automaticTax(ctx),
		RegistrationEnabled: s.registrationEnabled(ctx),
		StripeSet:           s.d.Settings.GetString(ctx, SettingStripeSecret, "") != "", WebhookSet: s.d.Settings.GetString(ctx, SettingWebhookSecret, "") != "",
		TestKeySet: s.d.Settings.GetString(ctx, SettingStripeTestKey, "") != "", TestWebhookSet: s.d.Settings.GetString(ctx, SettingWebhookTest, "") != "",
		StripeFromEnv: s.d.Config.Stripe.SecretKey != "", WebhookFromEnv: s.d.Config.Stripe.WebhookSecret != "",
		WebhookURL: base + "/webhooks/stripe", TestWebhookURL: base + "/webhooks/stripe/test", LiveWebhookURL: base + "/webhooks/stripe/live",
		Fields: map[string]string{},
	}
}

// settings is Commerce → Settings: currency, Stripe keys (set/clear; never
// shown) and the webhook endpoint to configure in the Stripe Dashboard.
func (h *adminHandlers) settings(w http.ResponseWriter, r *http.Request) {
	h.tpl.Render(w, r, "pages/commerce/settings.html", h.settingsData(r))
}

type settingsForm struct {
	Currency            string `form:"currency"`
	RegistrationPresent bool   `form:"registration_present"`
	RegistrationEnabled bool   `form:"registration_enabled"`
	StripeSecretKey     string `form:"stripe_secret_key"`
	WebhookSecret       string `form:"stripe_webhook_secret"`
	ClearStripeSecret   bool   `form:"clear_stripe_secret_key"`
	ClearWebhookSecret  bool   `form:"clear_stripe_webhook_secret"`
	StripeMode          string `form:"stripe_mode"`
	TestSecretKey       string `form:"stripe_test_secret_key"`
	TestWebhookSecret   string `form:"stripe_test_webhook_secret"`
	ClearTestSecret     bool   `form:"clear_stripe_test_secret_key"`
	ClearTestWebhook    bool   `form:"clear_stripe_test_webhook_secret"`
	GraceDays           string `form:"grace_days"`
	AutomaticTaxPresent bool   `form:"automatic_tax_present"`
	AutomaticTax        bool   `form:"automatic_tax"`
}

func (h *adminHandlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var f settingsForm
	_ = httpx.Bind(r, &f)
	cur := strings.ToUpper(strings.TrimSpace(f.Currency))
	if !validCurrency(cur) {
		pg := h.settingsData(r)
		pg.Currency, pg.Fields = f.Currency, map[string]string{"currency": "commerce.error.currency_invalid"}
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/commerce/settings.html", pg)
		return
	}
	changed := []string{}
	if f.StripeMode == "test" || f.StripeMode == "live" {
		if err := h.svc.d.Settings.Set(ctx, SettingStripeMode, f.StripeMode); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if f.GraceDays != "" {
		n, err := strconv.Atoi(strings.TrimSpace(f.GraceDays))
		if err != nil || n < 0 || n > 90 {
			pg := h.settingsData(r)
			pg.Fields = map[string]string{"grace_days": "commerce.error.grace_days"}
			h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/commerce/settings.html", pg)
			return
		}
		if err := h.svc.d.Settings.Set(ctx, SettingGraceDays, strconv.Itoa(n)); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if f.AutomaticTaxPresent {
		v := ""
		if f.AutomaticTax {
			v = "1"
		}
		if err := h.svc.d.Settings.Set(ctx, SettingAutomaticTax, v); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if f.RegistrationPresent {
		if err := h.svc.d.Settings.Set(ctx, "commerce.registration_enabled", f.RegistrationEnabled); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err := h.svc.d.Settings.Set(ctx, SettingCurrency, cur); err != nil {
		h.fail(w, r, err)
		return
	}
	for _, s := range []struct {
		key, value string
		clear      bool
	}{
		{SettingStripeSecret, strings.TrimSpace(f.StripeSecretKey), f.ClearStripeSecret},
		{SettingWebhookSecret, strings.TrimSpace(f.WebhookSecret), f.ClearWebhookSecret},
		{SettingStripeTestKey, strings.TrimSpace(f.TestSecretKey), f.ClearTestSecret},
		{SettingWebhookTest, strings.TrimSpace(f.TestWebhookSecret), f.ClearTestWebhook},
	} {
		switch {
		case s.clear:
			if err := h.svc.setSecretSetting(ctx, s.key, ""); err != nil {
				h.fail(w, r, err)
				return
			}
			changed = append(changed, s.key+":cleared")
		case s.value != "":
			if err := h.svc.setSecretSetting(ctx, s.key, s.value); err != nil {
				h.fail(w, r, err)
				return
			}
			changed = append(changed, s.key+":set")
		}
	}
	h.svc.audit.Record(ctx, "commerce.settings.save", "settings", "commerce", map[string]any{"currency": cur, "secrets": changed, "registration_policy_submitted": f.RegistrationPresent, "registration_enabled": h.svc.registrationEnabled(ctx)})
	httpx.SetFlash(w, "success", "commerce.flash.saved")
	http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
}

// sendTestEvent signs a local subscription.updated fixture with the test
// webhook secret and processes it. It does not call Stripe and never logs
// the secret.
func (h *adminHandlers) sendTestEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	secret := h.svc.webhookSecretFor(ctx, "test")
	if secret == "" {
		httpx.SetFlash(w, "error", "commerce.error.test_event")
		http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
		return
	}
	body, header := testSubscriptionEvent(secret, h.svc.now())
	if err := h.svc.handleStripeEvent(ctx, body, header, "test"); err != nil {
		h.svc.log.Error("commerce: test event", "err", err)
		httpx.SetFlash(w, "error", "commerce.error.test_event")
	} else {
		httpx.SetFlash(w, "success", "commerce.flash.test_event")
	}
	http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
}
