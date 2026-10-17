package redirects

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Perm is the permission for managing redirects.
const Perm = "redirects.manage"

// Module is the redirects kernel.Module.
type Module struct{}

// New returns the redirects module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "redirects" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes kernel.ServiceRedirects, wraps every public route with
// the redirect middleware, and adds the admin screen and API operations.
func (*Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(Perm, map[string]string{"en": "Manage redirects", "ar": "إدارة التحويلات"})
	svc := newService(d.DB, d.Hooks, d.Audit, d.Log)
	svc.siteHost = func(ctx context.Context) string { return HostOf(kernel.BaseURL(ctx, d)) }
	d.Services.Set(kernel.ServiceRedirects, Service(svc))
	d.Router.Public.Use(Middleware(svc))

	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("redirects: admin templates not registered (auth module must boot first)")
	}
	h := &adminHandlers{svc: svc, tpl: tpl, log: d.Log}
	r := d.Router.Admin.With(auth.RequirePerm(Perm))
	r.Get("/redirects", h.list)
	r.Post("/redirects", h.create)
	r.Post("/redirects/import", h.importCSV)
	r.Post("/redirects/{id}/toggle", h.toggle)
	r.Post("/redirects/{id}/delete", h.remove)
	d.Admin.AddNav(kernel.NavItem{ID: "redirects", LabelKey: "redirects.nav", Icon: "corner-up-right", Href: "/admin/redirects",
		Permission: Perm, Group: "structure", Position: 80})
	registerAPI(d, svc)
	return nil
}
