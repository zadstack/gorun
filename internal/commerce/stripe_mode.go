package commerce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v82"

	"github.com/zadstack/gorun/internal/settings"
)

// Stripe mode settings. commerce.stripe_mode stays "test" | "live" and an
// absent value still means live (stripeMode); bootstrapStripeMode pins the
// value explicitly once per store so that rule is only ever a fallback.
const (
	SettingSettingsVersion    = "commerce.settings_version" // bootstrap steps applied to this store
	settingStripeVerify       = "commerce.stripe_verify."   // + mode: last read-only key check (JSON)
	stripeModeSettingsVersion = "1"
)

// Authentication states of a mode's secret key. Presence of a key is
// "configured"; only a successful read-only call to Stripe with that exact
// key is "authenticated"; "mismatch" means Stripe answered but the key's
// livemode does not match the slot it sits in.
const (
	authUnverified    = "unverified"
	authAuthenticated = "authenticated"
	authFailed        = "failed"
	authMismatch      = "mismatch"
)

// keyVerification is what a read-only Stripe call reports about a key.
type keyVerification struct {
	Livemode bool
}

// keyVerifier authenticates a secret key against Stripe without writing
// anything (faked in tests).
type keyVerifier interface {
	Verify(ctx context.Context, secretKey string) (keyVerification, error)
}

// stripeVerifier reads /v1/balance: it needs a valid key, creates nothing and
// reports whether the key is a live-mode key.
type stripeVerifier struct{}

func (stripeVerifier) Verify(ctx context.Context, secretKey string) (keyVerification, error) {
	sc := stripe.NewClient(secretKey)
	b, err := sc.V1Balance.Retrieve(ctx, &stripe.BalanceRetrieveParams{})
	if err != nil {
		return keyVerification{}, err
	}
	return keyVerification{Livemode: b.Livemode}, nil
}

// storedVerification is the persisted result of the last key check for a
// mode. Fingerprint ties it to the key that was checked, so a replaced key
// is unverified again.
type storedVerification struct {
	Fingerprint string `json:"fingerprint"`
	OK          bool   `json:"ok"`
	Livemode    bool   `json:"livemode"`
	CheckedAt   string `json:"checked_at"`
	Error       string `json:"error,omitempty"`
}

type authStatus struct {
	State     string // authUnverified | authAuthenticated | authFailed | authMismatch
	CheckedAt string
	Error     string
}

type deliveryStatus struct {
	Verified bool   // at least one signed event from Stripe reached this mode's endpoint
	LastAt   string // received_at of the latest such event
	Count    int
}

// stripeModeStatus is one mode's three separate states: configured (keys
// present), authenticated (last read-only check) and webhook delivery
// verified (signed events from Stripe recorded for that mode).
type stripeModeStatus struct {
	Mode              string
	KeyConfigured     bool
	KeySource         string // settings | env | ""
	KeyPrefix         string // test | live | "" — a hint from the key prefix, never a verification
	WebhookConfigured bool
	WebhookSource     string // settings | env | ""
	Auth              authStatus
	Delivery          deliveryStatus
	Current           bool   // this is the active mode (set for the settings page)
	WebhookURL        string // this mode's public endpoint (set for the settings page)
}

func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// keyPrefixHint classifies a key by its documented prefix. It is only a hint
// for the operator: Stripe's livemode answer is the verification.
func keyPrefixHint(key string) string {
	switch {
	case strings.HasPrefix(key, "sk_test_") || strings.HasPrefix(key, "rk_test_"):
		return "test"
	case strings.HasPrefix(key, "sk_live_") || strings.HasPrefix(key, "rk_live_"):
		return "live"
	}
	return ""
}

func (s *service) stripeKeySource(ctx context.Context, mode string) string {
	switch {
	case mode == "test":
		if s.d.Settings.GetString(ctx, SettingStripeTestKey, "") != "" {
			return "settings"
		}
	case s.d.Settings.GetString(ctx, SettingStripeSecret, "") != "":
		return "settings"
	case s.d.Config.Stripe.SecretKey != "":
		return "env"
	}
	return ""
}

func (s *service) webhookSource(ctx context.Context, mode string) string {
	switch {
	case mode == "test":
		if s.d.Settings.GetString(ctx, SettingWebhookTest, "") != "" {
			return "settings"
		}
	case s.d.Settings.GetString(ctx, SettingWebhookSecret, "") != "":
		return "settings"
	case s.d.Config.Stripe.WebhookSecret != "":
		return "env"
	}
	return ""
}

func (s *service) loadVerification(ctx context.Context, mode string) (storedVerification, bool) {
	raw := s.d.Settings.GetString(ctx, settingStripeVerify+mode, "")
	if raw == "" {
		return storedVerification{}, false
	}
	var v storedVerification
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return storedVerification{}, false
	}
	return v, true
}

func (s *service) storeVerification(ctx context.Context, mode string, v storedVerification) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.d.Settings.Set(ctx, settingStripeVerify+mode, string(b))
}

// authStatusFor derives the authentication state of key in mode from the
// stored check; a result for another key counts for nothing.
func (s *service) authStatusFor(ctx context.Context, mode, key string) authStatus {
	if key == "" {
		return authStatus{State: authUnverified}
	}
	v, ok := s.loadVerification(ctx, mode)
	if !ok || v.Fingerprint != keyFingerprint(key) {
		return authStatus{State: authUnverified}
	}
	st := authStatus{CheckedAt: v.CheckedAt, Error: v.Error}
	switch {
	case !v.OK:
		st.State = authFailed
	case v.Livemode != (mode == "live"):
		st.State = authMismatch
	default:
		st.State = authAuthenticated
	}
	return st
}

// stripeStatus reports the three states of mode. It never calls Stripe.
func (s *service) stripeStatus(ctx context.Context, mode string) stripeModeStatus {
	key := s.stripeSecretFor(ctx, mode)
	st := stripeModeStatus{Mode: mode, KeyConfigured: key != "", KeySource: s.stripeKeySource(ctx, mode), KeyPrefix: keyPrefixHint(key),
		WebhookConfigured: s.webhookSecretFor(ctx, mode) != "", WebhookSource: s.webhookSource(ctx, mode)}
	st.Auth = s.authStatusFor(ctx, mode, key)
	var n int
	var last string
	if err := s.db.QueryRowContext(ctx, `SELECT count(*), COALESCE(MAX(received_at), '') FROM stripe_events WHERE mode = ? AND source = 'stripe'`, mode).Scan(&n, &last); err == nil {
		st.Delivery = deliveryStatus{Verified: n > 0, LastAt: last, Count: n}
	}
	return st
}

// verifyStripeKey runs the read-only check for mode's key and stores the
// result. It returns the derived state; ErrStripeNotConfigured when the mode
// has no key (nothing is called).
func (s *service) verifyStripeKey(ctx context.Context, mode string) (authStatus, error) {
	key := s.stripeSecretFor(ctx, mode)
	if key == "" {
		return authStatus{State: authUnverified}, ErrStripeNotConfigured
	}
	return s.verifyKeyAs(ctx, mode, key)
}

// verifyKeyAs checks key as mode's key and records the outcome under that
// key's fingerprint.
func (s *service) verifyKeyAs(ctx context.Context, mode, key string) (authStatus, error) {
	v := storedVerification{Fingerprint: keyFingerprint(key), CheckedAt: s.nowString()}
	res, err := s.verifier.Verify(ctx, key)
	if err != nil {
		v.Error = truncate(err.Error(), 200)
	} else {
		v.OK, v.Livemode = true, res.Livemode
	}
	if err := s.storeVerification(ctx, mode, v); err != nil {
		return authStatus{}, err
	}
	s.audit.Record(ctx, "commerce.stripe.verify", "settings", "commerce", map[string]any{"mode": mode, "ok": v.OK, "livemode": v.Livemode, "key_source": s.stripeKeySource(ctx, mode)})
	return s.authStatusFor(ctx, mode, key), nil
}

// modeSwitchInput is what the settings form would make effective for each
// slot: the submitted value, else the stored/env value, else "" when cleared.
type modeSwitchInput struct {
	LiveKey, LiveWebhook, TestKey string
	Confirmed                     bool
}

// modeSwitchError is a refused switch: Field is the i18n key shown on the
// mode control. Detail (never a secret) is appended for failed checks.
type modeSwitchError struct {
	Field, Detail string
}

func (e *modeSwitchError) Error() string { return "commerce: mode switch refused: " + e.Field }

// validateModeSwitch checks that target is safe to enable with the keys in
// in. Switching to test never calls Stripe and only refuses a live-prefixed
// key in the test slot. Switching to live needs explicit confirmation, a
// live key that is not test-prefixed, a live webhook secret, and a
// successful read-only authentication reporting livemode=true; the check's
// result is stored so Settings shows it. No charge or other write happens.
func (s *service) validateModeSwitch(ctx context.Context, target string, in modeSwitchInput) error {
	switch target {
	case "test":
		if keyPrefixHint(in.TestKey) == "live" {
			return &modeSwitchError{Field: "commerce.error.mode_test_key_live"}
		}
		return nil
	case "live":
		if !in.Confirmed {
			return &modeSwitchError{Field: "commerce.error.mode_confirm"}
		}
		if in.LiveKey == "" {
			return &modeSwitchError{Field: "commerce.error.mode_live_key_missing"}
		}
		if keyPrefixHint(in.LiveKey) == "test" {
			return &modeSwitchError{Field: "commerce.error.mode_live_key_test"}
		}
		if in.LiveWebhook == "" {
			return &modeSwitchError{Field: "commerce.error.mode_live_webhook_missing"}
		}
		st, err := s.verifyKeyAs(ctx, "live", in.LiveKey)
		if err != nil {
			return err
		}
		switch st.State {
		case authAuthenticated:
			return nil
		case authMismatch:
			return &modeSwitchError{Field: "commerce.error.mode_live_key_test"}
		default:
			return &modeSwitchError{Field: "commerce.error.mode_live_verify_failed", Detail: st.Error}
		}
	}
	return &modeSwitchError{Field: "commerce.error.mode_invalid"}
}

// bootstrapStripeMode pins commerce.stripe_mode once per store (versioned by
// commerce.settings_version). A store that has never sold anything and has
// no Stripe secret anywhere starts in test mode; any other store is pinned
// to the mode it already runs in (live when the setting is absent), so an
// existing deployment never changes behaviour. The absent-means-live
// fallback in stripeMode is untouched.
func bootstrapStripeMode(ctx context.Context, s *service) error {
	if s.d.Settings.GetString(ctx, SettingSettingsVersion, "") >= stripeModeSettingsVersion {
		return nil
	}
	var stored string
	err := s.d.Settings.Get(ctx, SettingStripeMode, &stored)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return fmt.Errorf("commerce: read stripe mode: %w", err)
	}
	mode, reason := s.stripeMode(ctx), "existing_setting"
	if stored == "" {
		fresh, err := s.storeIsNew(ctx)
		if err != nil {
			return err
		}
		if fresh {
			mode, reason = "test", "new_store"
		} else {
			reason = "existing_store"
		}
		if err := s.d.Settings.Set(ctx, SettingStripeMode, mode); err != nil {
			return fmt.Errorf("commerce: pin stripe mode: %w", err)
		}
	}
	if err := s.d.Settings.Set(ctx, SettingSettingsVersion, stripeModeSettingsVersion); err != nil {
		return fmt.Errorf("commerce: settings version: %w", err)
	}
	s.audit.Record(ctx, "commerce.settings.bootstrap", "settings", "commerce", map[string]any{"version": stripeModeSettingsVersion, "stripe_mode": mode, "reason": reason})
	return nil
}

// storeIsNew is true when nothing suggests the store has run before: no
// orders and no Stripe secret in settings or the environment.
func (s *service) storeIsNew(ctx context.Context) (bool, error) {
	var orders int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM orders`).Scan(&orders); err != nil {
		return false, fmt.Errorf("commerce: count orders: %w", err)
	}
	if orders > 0 || s.d.Config.Stripe.SecretKey != "" || s.d.Config.Stripe.WebhookSecret != "" {
		return false, nil
	}
	for _, k := range []string{SettingStripeSecret, SettingWebhookSecret, SettingStripeTestKey, SettingWebhookTest} {
		if s.d.Settings.GetString(ctx, k, "") != "" {
			return false, nil
		}
	}
	return true, nil
}

// stripeKeyForOrder is the key of the order's recorded mode; an order from
// before provenance was recorded falls back to the active mode's key.
func (s *service) stripeKeyForOrder(ctx context.Context, o *Order) string {
	if o.StripeMode == "test" || o.StripeMode == "live" {
		return s.stripeSecretFor(ctx, o.StripeMode)
	}
	return s.stripeSecretKey(ctx)
}
