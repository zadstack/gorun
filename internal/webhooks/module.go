// Package webhooks sends signed HTTP notifications of site events (content,
// media, menus, forms, sign-ins, orders, migrations) to subscribed endpoints,
// with retries, a delivery log, redelivery and secret rotation.
package webhooks

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Perm is the permission for managing webhooks.
const Perm = "webhooks.manage"

// sendTimeout bounds one delivery attempt.
const sendTimeout = 10 * time.Second

// Service manages webhooks (kernel.ServiceWebhooks).
type Service interface {
	List(ctx context.Context) ([]Webhook, error)
	Get(ctx context.Context, id string) (*Webhook, error)
	Create(ctx context.Context, in Input) (*Webhook, string, error)
	Update(ctx context.Context, id string, in Input) (*Webhook, error)
	Delete(ctx context.Context, id string) error
	RotateSecret(ctx context.Context, id string) (string, error)
	Test(ctx context.Context, id string) (*Delivery, error)
	Deliveries(ctx context.Context, webhookID string, limit int) ([]Delivery, error)
	Redeliver(ctx context.Context, deliveryID string) (*Delivery, error)
}

// deliveryClient posts deliveries: private addresses refused (unless
// allowPrivate) and no redirects followed, so a signed body only ever goes
// to the configured URL.
func deliveryClient(allowPrivate bool) *http.Client {
	return httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: allowPrivate, Timeout: sendTimeout, NoRedirects: true})
}

// Module is the webhooks kernel.Module.
type Module struct{}

// New returns the webhooks module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "webhooks" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes the service, subscribes to every catalog event,
// registers the delivery job and the daily prune, and adds the admin screens
// and API operations.
func (*Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(Perm, map[string]string{"en": "Manage webhooks", "ar": "إدارة الويبهوكس"})
	allowPrivate := d.Config.AllowPrivateNet || d.Config.Env == "dev"
	svc := &service{db: d.DB, jobs: d.Jobs, audit: d.Audit, log: d.Log, key: d.Config.SecretKey, allowPrivate: allowPrivate,
		baseURL: func(ctx context.Context) string { return kernel.BaseURL(ctx, d) }, now: time.Now,
		send: httpSender(deliveryClient(allowPrivate))}
	d.Services.Set(kernel.ServiceWebhooks, Service(svc))
	for _, e := range events {
		name := e.Name
		d.Hooks.Action(name, 1000, func(ctx context.Context, payload any) error { return svc.dispatch(ctx, name, payload) })
	}
	if d.Jobs != nil {
		d.Jobs.Register(deliverKind, svc.deliverHandler, maxAttempts)
		d.Jobs.Cron("webhooks.prune", 24*time.Hour, func(ctx context.Context) error { _, err := svc.Prune(ctx); return err })
	}
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("webhooks: admin templates not registered (auth module must boot first)")
	}
	h := &adminHandlers{svc: svc, tpl: tpl, log: d.Log}
	r := d.Router.Admin.With(auth.RequirePerm(Perm))
	r.Get("/webhooks", h.list)
	r.Post("/webhooks", h.create)
	r.Get("/webhooks/{id}", h.detail)
	r.Post("/webhooks/{id}", h.update)
	r.Post("/webhooks/{id}/test", h.test)
	r.Post("/webhooks/{id}/secret", h.rotate)
	r.Post("/webhooks/{id}/delete", h.remove)
	r.Post("/webhooks/deliveries/{id}/redeliver", h.redeliver)
	d.Admin.AddNav(kernel.NavItem{ID: "webhooks", LabelKey: "webhooks.nav", Icon: "webhook", Href: "/admin/webhooks",
		Permission: Perm, Group: "integrations", Position: 810})
	registerAPI(d, svc)
	return nil
}
