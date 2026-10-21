package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/media"
)

// Errors returned by the service.
var (
	ErrNotFound            = errors.New("commerce: not found")
	ErrStripeNotConfigured = errors.New("commerce: Stripe is not configured")
	ErrInvalidSignature    = errors.New("commerce: invalid Stripe webhook signature")
	ErrNotActive           = errors.New("commerce: entitlement is not active")
	ErrNoServer            = errors.New("commerce: entitlement has no reachable billing server")
	ErrPeriodEnded         = errors.New("commerce: the entitlement period has ended")
	ErrPlanRequired        = errors.New("commerce: subscription plan required")
)

// ValidationError carries per-field i18n keys (implements api.FieldErrorer).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("commerce: invalid input: %v", e.Fields) }

// FieldErrors implements api.FieldErrorer.
func (e *ValidationError) FieldErrors() map[string]string { return maps.Clone(e.Fields) }

// Setting keys.
const (
	SettingCurrency      = "commerce.currency"
	SettingStripeSecret  = "commerce.stripe_secret_key"     // live key; db.Encrypt, AAD = key
	SettingWebhookSecret = "commerce.stripe_webhook_secret" // live webhook; db.Encrypt, AAD = key
	SettingStripeMode    = "commerce.stripe_mode"           // test | live
	SettingStripeTestKey = "commerce.stripe_test_secret_key"
	SettingWebhookTest   = "commerce.stripe_test_webhook_secret"
	SettingGraceDays     = "commerce.grace_days"
	SettingAutomaticTax  = "commerce.automatic_tax"
	defaultCurrency      = "AED"
	defaultGraceDays     = 7
)

// Permissions.
const (
	PermManage     = "commerce.manage"
	PermViewOrders = "commerce.view_orders"
)

// Job kinds and hook names.
const (
	JobProvision            = "commerce.provision"
	JobSuspend              = "commerce.suspend"
	JobMagicMail            = "commerce.magic_mail"
	JobBillingMail          = "commerce.billing_mail"
	JobResume               = "commerce.resume"
	CronExpire              = "commerce.expire"
	CronGrace               = "commerce.billing_grace"
	HookOrderPaid           = "commerce.order.paid"
	HookEntitlementActivate = "commerce.entitlement.activated"
	HookSubActivated        = "subscription.activated"
	HookSubPastDue          = "subscription.past_due"
	HookSubCanceled         = "subscription.canceled"
	provisionMaxAttempts    = 8
)

// mailFunc sends one email (mailer.Mailer.Send).
type mailFunc func(ctx context.Context, to, subject, html, text string) error

// service implements Service.
type service struct {
	d           *kernel.Deps
	db          *sql.DB
	log         *slog.Logger
	audit       *audit.Writer
	checkout    checkoutCreator
	portal      portalCreator
	refunds     refundCreator
	sendMail    mailFunc
	sendReceipt mailFunc
	now         func() time.Time
	health      *healthCache
}

func newService(d *kernel.Deps, checkout checkoutCreator, send mailFunc) *service {
	s := &service{d: d, db: d.DB, log: d.Log, audit: d.Audit, checkout: checkout, sendMail: send, now: time.Now, health: newHealthCache()}
	if s.checkout == nil {
		s.checkout = stripeCheckout{}
	}
	if s.portal == nil {
		s.portal = stripePortal{}
	}
	if s.refunds == nil {
		s.refunds = stripeRefunds{}
	}
	if s.sendMail == nil {
		s.sendMail = func(ctx context.Context, to, subject, html, text string) error {
			m, ok := kernel.Get[*mailer.Mailer](d, mailer.ServiceName)
			if !ok {
				return errors.New("commerce: mailer service not registered")
			}
			return m.Send(ctx, to, subject, html, text)
		}
	}
	s.sendReceipt = send
	if s.sendReceipt == nil {
		s.sendReceipt = func(ctx context.Context, to, subject, html, text string) error {
			m, ok := kernel.Get[*mailer.Mailer](d, mailer.ServiceName)
			if !ok {
				return errors.New("commerce: mailer service not registered")
			}
			return m.SendRequired(ctx, to, subject, html, text)
		}
	}
	return s
}

func (s *service) users() (auth.Service, bool) {
	return kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
}
func (s *service) media() (media.Service, bool) {
	return kernel.Get[media.Service](s.d, kernel.ServiceMedia)
}

// baseURL is the public site URL (kernel.BaseURL).
func (s *service) baseURL(ctx context.Context) string { return kernel.BaseURL(ctx, s.d) }

// currency is the store currency (setting commerce.currency, default AED).
func (s *service) currency(ctx context.Context) string {
	c := strings.ToUpper(strings.TrimSpace(s.d.Settings.GetString(ctx, SettingCurrency, defaultCurrency)))
	if !validCurrency(c) {
		return defaultCurrency
	}
	return c
}

// secretSetting decrypts an encrypted setting; fallback applies when unset.
func (s *service) secretSetting(ctx context.Context, key, fallback string) string {
	enc := s.d.Settings.GetString(ctx, key, "")
	if enc == "" {
		return fallback
	}
	plain, err := db.Decrypt(s.d.Config.SecretKey, enc, key)
	if err != nil {
		s.log.Error("commerce: decrypt setting", "key", key, "err", err)
		return fallback
	}
	return string(plain)
}

// stripeMode is "test" or "live". Unset means live so existing one-time
// stores keep using the original secret.
func (s *service) stripeMode(ctx context.Context) string {
	if s.d.Settings.GetString(ctx, SettingStripeMode, "live") == "test" {
		return "test"
	}
	return "live"
}

func (s *service) stripeSecretKey(ctx context.Context) string {
	return s.stripeSecretFor(ctx, s.stripeMode(ctx))
}

func (s *service) stripeSecretFor(ctx context.Context, mode string) string {
	if mode == "test" {
		return s.secretSetting(ctx, SettingStripeTestKey, "")
	}
	return s.secretSetting(ctx, SettingStripeSecret, s.d.Config.Stripe.SecretKey)
}

func (s *service) webhookSecret(ctx context.Context) string {
	return s.webhookSecretFor(ctx, s.stripeMode(ctx))
}

func (s *service) webhookSecretFor(ctx context.Context, mode string) string {
	if mode == "test" {
		return s.secretSetting(ctx, SettingWebhookTest, "")
	}
	return s.secretSetting(ctx, SettingWebhookSecret, s.d.Config.Stripe.WebhookSecret)
}

func (s *service) graceDays(ctx context.Context) int {
	raw := strings.TrimSpace(s.d.Settings.GetString(ctx, SettingGraceDays, ""))
	if raw == "" {
		return defaultGraceDays
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 90 {
		return defaultGraceDays
	}
	return n
}

func (s *service) automaticTax(ctx context.Context) bool {
	return s.d.Settings.GetString(ctx, SettingAutomaticTax, "") == "1"
}

// setSecretSetting stores plain encrypted under key ("" clears it).
func (s *service) setSecretSetting(ctx context.Context, key, plain string) error {
	if plain == "" {
		return s.d.Settings.Set(ctx, key, "")
	}
	enc, err := db.Encrypt(s.d.Config.SecretKey, []byte(plain), key)
	if err != nil {
		return err
	}
	return s.d.Settings.Set(ctx, key, enc)
}

// siteLocale is the public site locale (setting site.locale, default ar).
func (s *service) siteLocale(ctx context.Context) string {
	return s.d.Settings.GetString(ctx, "site.locale", "ar")
}

func (s *service) nowString() string { return db.FormatTime(s.now()) }

// jsonText marshals v for a JSON TEXT column ("{}"/"[]" defaults on nil).
func jsonText(v any, def string) string {
	if v == nil {
		return def
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return def
	}
	return string(b)
}

// nullable maps "" to SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// pageBounds normalises page/perPage (defaults 1/20, max 100).
func pageBounds(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	return page, min(perPage, 100)
}

// likePattern escapes s for LIKE … ESCAPE '\'.
func likePattern(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

var (
	zeroDecimalCurrencies  = []string{"BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF"}
	threeDecimalCurrencies = []string{"BHD", "JOD", "KWD", "OMR", "TND"}
)

// minorDigits is the number of decimals of currency's minor unit.
func minorDigits(currency string) int {
	switch c := strings.ToUpper(currency); {
	case slices.Contains(zeroDecimalCurrencies, c):
		return 0
	case slices.Contains(threeDecimalCurrencies, c):
		return 3
	default:
		return 2
	}
}
