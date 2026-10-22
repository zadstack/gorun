package commerce

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

type settingsPage struct {
	Currency                      string
	Mode                          string // the current (stored) mode: the badge
	Selected                      string // the select's value (the submitted target on a refused save)
	GraceDays                     int
	AutomaticTax                  bool
	RegistrationEnabled           bool
	StripeSet, WebhookSet         bool
	TestKeySet, TestWebhookSet    bool
	StripeFromEnv, WebhookFromEnv bool
	WebhookURL                    string
	TestWebhookURL                string
	LiveWebhookURL                string
	Live, Test                    stripeModeStatus // the three states of each mode
	ModeDetail                    string           // detail of a refused switch (never a secret)
	Fields                        map[string]string
}

func (h *adminHandlers) settingsData(r *http.Request) settingsPage {
	ctx := r.Context()
	s := h.svc
	base := s.baseURL(ctx)
	mode := s.stripeMode(ctx)
	live, test := s.stripeStatus(ctx, "live"), s.stripeStatus(ctx, "test")
	live.Current, live.WebhookURL = mode == "live", base+"/webhooks/stripe/live"
	test.Current, test.WebhookURL = mode == "test", base+"/webhooks/stripe/test"
	return settingsPage{
		Currency: s.currency(ctx), Mode: mode, Selected: mode, GraceDays: s.graceDays(ctx), AutomaticTax: s.automaticTax(ctx),
		RegistrationEnabled: s.registrationEnabled(ctx),
		StripeSet:           s.d.Settings.GetString(ctx, SettingStripeSecret, "") != "", WebhookSet: s.d.Settings.GetString(ctx, SettingWebhookSecret, "") != "",
		TestKeySet: s.d.Settings.GetString(ctx, SettingStripeTestKey, "") != "", TestWebhookSet: s.d.Settings.GetString(ctx, SettingWebhookTest, "") != "",
		StripeFromEnv: s.d.Config.Stripe.SecretKey != "", WebhookFromEnv: s.d.Config.Stripe.WebhookSecret != "",
		WebhookURL: base + "/webhooks/stripe", TestWebhookURL: base + "/webhooks/stripe/test", LiveWebhookURL: base + "/webhooks/stripe/live",
		Live: live, Test: test,
		Fields: map[string]string{},
	}
}

// settings is Commerce → Settings: currency, Stripe keys (set/clear; never
// shown), the current mode with each mode's three states, and the webhook
// endpoints to configure in the Stripe Dashboard.
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
	ConfirmLive         bool   `form:"confirm_live"`
	TestSecretKey       string `form:"stripe_test_secret_key"`
	TestWebhookSecret   string `form:"stripe_test_webhook_secret"`
	ClearTestSecret     bool   `form:"clear_stripe_test_secret_key"`
	ClearTestWebhook    bool   `form:"clear_stripe_test_webhook_secret"`
	GraceDays           string `form:"grace_days"`
	AutomaticTaxPresent bool   `form:"automatic_tax_present"`
	AutomaticTax        bool   `form:"automatic_tax"`
}

// effective is the value a slot would hold after this save: the submitted
// value, "" when cleared, else what is stored (or the environment).
func effective(submitted string, clear bool, stored string) string {
	switch {
	case clear:
		return ""
	case submitted != "":
		return submitted
	}
	return stored
}

func (h *adminHandlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var f settingsForm
	_ = httpx.Bind(r, &f)
	f.StripeSecretKey, f.WebhookSecret = strings.TrimSpace(f.StripeSecretKey), strings.TrimSpace(f.WebhookSecret)
	f.TestSecretKey, f.TestWebhookSecret = strings.TrimSpace(f.TestSecretKey), strings.TrimSpace(f.TestWebhookSecret)
	reject := func(field, key, detail string) {
		pg := h.settingsData(r)
		pg.Currency, pg.ModeDetail = f.Currency, detail
		if f.StripeMode == "test" || f.StripeMode == "live" {
			pg.Selected = f.StripeMode
		}
		pg.Fields = map[string]string{field: key}
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/commerce/settings.html", pg)
	}
	cur := strings.ToUpper(strings.TrimSpace(f.Currency))
	if !validCurrency(cur) {
		reject("currency", "commerce.error.currency_invalid", "")
		return
	}
	// A key whose prefix contradicts its slot is refused on every save,
	// whatever the current mode and whether or not the mode changes.
	if key := slotKeyError("live", f.StripeSecretKey); key != "" {
		reject("stripe_secret_key", key, "")
		return
	}
	if key := slotKeyError("test", f.TestSecretKey); key != "" {
		reject("stripe_test_secret_key", key, "")
		return
	}
	grace := -1
	if f.GraceDays != "" {
		n, err := strconv.Atoi(strings.TrimSpace(f.GraceDays))
		if err != nil || n < 0 || n > 90 {
			reject("grace_days", "commerce.error.grace_days", "")
			return
		}
		grace = n
	}
	// A mode change is validated before anything is stored, against the
	// keys this save would make effective, so a refused switch saves nothing.
	s := h.svc
	current := s.stripeMode(ctx)
	switching := (f.StripeMode == "test" || f.StripeMode == "live") && f.StripeMode != current
	if switching {
		in := modeSwitchInput{
			Confirmed:     f.ConfirmLive,
			LiveKey:       effective(f.StripeSecretKey, f.ClearStripeSecret, s.secretSetting(ctx, SettingStripeSecret, "")),
			LiveKeySource: "settings",
			LiveWebhook:   effective(f.WebhookSecret, f.ClearWebhookSecret, s.secretSetting(ctx, SettingWebhookSecret, "")),
			TestKey:       effective(f.TestSecretKey, f.ClearTestSecret, s.secretSetting(ctx, SettingStripeTestKey, "")),
		}
		if f.StripeSecretKey != "" && !f.ClearStripeSecret {
			in.LiveKeySource = "form"
		}
		// A cleared or empty stored live slot falls back to the environment.
		if in.LiveKey == "" {
			in.LiveKey, in.LiveKeySource = s.d.Config.Stripe.SecretKey, "env"
		}
		if in.LiveWebhook == "" {
			in.LiveWebhook = s.d.Config.Stripe.WebhookSecret
		}
		var refused *modeSwitchError
		switch err := s.validateModeSwitch(ctx, f.StripeMode, in); {
		case errors.As(err, &refused):
			s.audit.Record(ctx, "commerce.settings.mode_refused", "settings", "commerce", map[string]any{"from": current, "to": f.StripeMode, "reason": refused.Field})
			reject("stripe_mode", refused.Field, refused.Detail)
			return
		case err != nil:
			h.fail(w, r, err)
			return
		}
	}
	changed := []string{}
	if grace >= 0 {
		if err := s.d.Settings.Set(ctx, SettingGraceDays, strconv.Itoa(grace)); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if f.AutomaticTaxPresent {
		v := ""
		if f.AutomaticTax {
			v = "1"
		}
		if err := s.d.Settings.Set(ctx, SettingAutomaticTax, v); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if f.RegistrationPresent {
		if err := s.d.Settings.Set(ctx, "commerce.registration_enabled", f.RegistrationEnabled); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err := s.d.Settings.Set(ctx, SettingCurrency, cur); err != nil {
		h.fail(w, r, err)
		return
	}
	for _, sec := range []struct {
		key, value string
		clear      bool
	}{
		{SettingStripeSecret, f.StripeSecretKey, f.ClearStripeSecret},
		{SettingWebhookSecret, f.WebhookSecret, f.ClearWebhookSecret},
		{SettingStripeTestKey, f.TestSecretKey, f.ClearTestSecret},
		{SettingWebhookTest, f.TestWebhookSecret, f.ClearTestWebhook},
	} {
		switch {
		case sec.clear:
			if err := s.setSecretSetting(ctx, sec.key, ""); err != nil {
				h.fail(w, r, err)
				return
			}
			changed = append(changed, sec.key+":cleared")
		case sec.value != "":
			if err := s.setSecretSetting(ctx, sec.key, sec.value); err != nil {
				h.fail(w, r, err)
				return
			}
			changed = append(changed, sec.key+":set")
		}
	}
	if switching {
		if err := s.d.Settings.Set(ctx, SettingStripeMode, f.StripeMode); err != nil {
			h.fail(w, r, err)
			return
		}
		s.audit.Record(ctx, "commerce.settings.mode", "settings", "commerce", map[string]any{"from": current, "to": f.StripeMode, "confirmed": f.ConfirmLive})
	}
	s.audit.Record(ctx, "commerce.settings.save", "settings", "commerce", map[string]any{"currency": cur, "secrets": changed, "stripe_mode": s.stripeMode(ctx), "registration_policy_submitted": f.RegistrationPresent, "registration_enabled": s.registrationEnabled(ctx)})
	switch {
	case switching && f.StripeMode == "live":
		httpx.SetFlash(w, "success", "commerce.flash.mode_live")
	case switching:
		httpx.SetFlash(w, "success", "commerce.flash.mode_test")
	default:
		httpx.SetFlash(w, "success", "commerce.flash.saved")
	}
	http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
}

// verifyKey is POST /admin/commerce/settings/verify: a read-only Stripe call
// with the chosen mode's key. It records authenticated / failed / mismatch
// for that key; it never creates a charge and never proves webhook delivery.
func (h *adminHandlers) verifyKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mode := r.PostFormValue("mode")
	if mode != "test" && mode != "live" {
		pg := h.settingsData(r)
		pg.Fields = map[string]string{"verify": "commerce.error.mode_invalid"}
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/commerce/settings.html", pg)
		return
	}
	st, err := h.svc.verifyStripeKey(ctx, mode)
	switch {
	case errors.Is(err, ErrStripeNotConfigured):
		httpx.SetFlash(w, "error", "commerce.error.verify_no_key")
	case err != nil:
		h.fail(w, r, err)
		return
	case st.State == authAuthenticated:
		httpx.SetFlash(w, "success", "commerce.flash.verify_ok")
	case st.State == authMismatch:
		httpx.SetFlash(w, "error", "commerce.error.verify_mismatch")
	default:
		httpx.SetFlash(w, "error", "commerce.error.verify_failed")
	}
	http.Redirect(w, r, "/admin/commerce/settings#mode", http.StatusSeeOther)
}

// sendTestEvent signs a local subscription.updated fixture with the test
// webhook secret and processes it. It does not call Stripe, never logs the
// secret, and is recorded as a local event: it is not delivery evidence.
func (h *adminHandlers) sendTestEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	secret := h.svc.webhookSecretFor(ctx, "test")
	if secret == "" {
		httpx.SetFlash(w, "error", "commerce.error.test_event")
		http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
		return
	}
	body, header := testSubscriptionEvent(secret, h.svc.now())
	if err := h.svc.handleStripeEventFrom(ctx, body, header, "test", "local"); err != nil {
		h.svc.log.Error("commerce: test event", "err", err)
		httpx.SetFlash(w, "error", "commerce.error.test_event")
	} else {
		httpx.SetFlash(w, "success", "commerce.flash.test_event")
	}
	http.Redirect(w, r, "/admin/commerce/settings", http.StatusSeeOther)
}
