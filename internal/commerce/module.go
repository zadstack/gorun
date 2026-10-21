// Package commerce sells access to MCP servers: products → Stripe Checkout
// → orders → customers → entitlements → provisioning on billing servers
// over the signed mcp-billing/v1 protocol (spec §4.6, docs/COMMERCE.md).
package commerce

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the commerce kernel.Module.
type Module struct {
	checkout checkoutCreator // nil: real Stripe
	sendMail mailFunc        // nil: the mailer service
	svc      *service
}

// New returns the commerce module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "commerce" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes kernel.ServiceCommerce and wires jobs, theme kinds and
// funcs, public routes, the Stripe webhook, admin screens, API operations
// and health checks.
func (m *Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(PermManage, map[string]string{"en": "Manage the store", "ar": "إدارة المتجر"})
	d.Perms.Declare(PermViewOrders, map[string]string{"en": "View orders", "ar": "عرض الطلبات"})

	s := newService(d, m.checkout, m.sendMail)
	m.svc = s
	d.Services.Set(kernel.ServiceCommerce, Service(s))

	d.Jobs.Register(JobProvision, s.provision, provisionMaxAttempts)
	d.Jobs.Register(JobSuspend, s.suspendJob, provisionMaxAttempts)
	d.Jobs.Register(JobMagicMail, s.magicMail, 3)
	d.Jobs.Cron(CronExpire, expireEvery, s.expire)
	d.Jobs.Cron("commerce.request_receipts", 10*time.Second, s.deliverRequestReceipts)

	registerTheme(d, s)
	h := &publicHandlers{svc: s, limiter: newRateLimiter()}
	pages := d.Router.Public.With(h.siteLocale, auth.CSRFMiddleware())
	get := func(r chi.Router, pattern string, fn http.HandlerFunc) {
		r.Get(pattern, fn)
		r.Head(pattern, fn)
	}
	get(pages, "/cart", h.cartPage)
	get(pages, "/checkout", h.cartPage)
	get(pages, "/cart/", slashless)
	get(pages, "/checkout/", slashless)
	pages.Post("/cart/items", h.cartAdd)
	pages.Post("/cart/remove", h.cartRemove)
	pages.Post("/cart/clear", h.cartClear)
	pages.Post("/checkout", h.cartCheckout)
	get(pages, "/account/download/{order}/{item}", h.download)
	get(pages, "/shop", h.shop)
	get(pages, "/product/{slug}", h.product)
	get(pages, "/shop/", slashless)
	get(pages, "/product/{slug}/", slashless)
	pages.Post("/checkout/{slug}", h.handleCheckout)
	get(pages, "/checkout/success", h.handleSuccess)
	get(pages, "/checkout/status/{id}", h.handleStatus)
	get(pages, "/account", h.account)
	pages.Post("/account/login", h.accountLogin)
	pages.Post("/account/password-login", h.accountPasswordLogin)
	pages.Post("/account/register", h.accountRegister)
	pages.Post("/account/open/{entitlement}", h.accountOpen)
	d.Router.Webhooks.Post("/stripe", h.handleStripeWebhook)

	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("commerce: admin templates not registered (auth module must boot first)")
	}
	registerAdmin(d, s, tpl)
	registerAPI(d, s)
	d.Hooks.Filter(admin.HealthCheckFilter, 50, s.healthChecks)
	if err := seedAccountRedirect(context.Background(), d); err != nil {
		d.Log.Warn("commerce: default /my-account redirect not created", "err", err)
	}
	if err := seedExampleProducts(context.Background(), s); err != nil {
		return fmt.Errorf("commerce: example products: %w", err)
	}
	return nil
}
