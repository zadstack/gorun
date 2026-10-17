package commerce

import (
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

type settingsPage struct {
	Currency                      string
	RegistrationEnabled           bool
	StripeSet, WebhookSet         bool
	StripeFromEnv, WebhookFromEnv bool
	WebhookURL                    string
	Fields                        map[string]string
}

func (h *adminHandlers) settingsData(r *http.Request) settingsPage {
	ctx := r.Context()
	s := h.svc
	return settingsPage{
		Currency:            s.currency(ctx),
		RegistrationEnabled: s.registrationEnabled(ctx),
		StripeSet:           s.d.Settings.GetString(ctx, SettingStripeSecret, "") != "", WebhookSet: s.d.Settings.GetString(ctx, SettingWebhookSecret, "") != "",
		StripeFromEnv: s.d.Config.Stripe.SecretKey != "", WebhookFromEnv: s.d.Config.Stripe.WebhookSecret != "",
		WebhookURL: s.baseURL(ctx) + "/webhooks/stripe", Fields: map[string]string{},
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
	}{{SettingStripeSecret, strings.TrimSpace(f.StripeSecretKey), f.ClearStripeSecret}, {SettingWebhookSecret, strings.TrimSpace(f.WebhookSecret), f.ClearWebhookSecret}} {
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
