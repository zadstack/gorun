package seo

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the seo kernel.Module. It owns no tables (settings only) and
// mounts no public routes: the site/theme module serves /sitemap.xml and
// /robots.txt through the Service.
type Module struct{}

// New returns the seo module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "seo" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return nil }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes kernel.ServiceSEO and the Settings → SEO screen.
func (*Module) Register(d *kernel.Deps) error {
	svc := &service{settings: d.Settings, hooks: d.Hooks, log: d.Log, baseURL: d.Config.BaseURL,
		content:  func() (content.Service, bool) { return kernel.Get[content.Service](d, kernel.ServiceContent) },
		taxonomy: func() (taxonomy.Service, bool) { return kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy) },
	}
	d.Services.Set(kernel.ServiceSEO, Service(svc))

	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("seo: admin templates not registered (auth module must boot first)")
	}
	h := &settingsHandlers{deps: d, svc: svc, tpl: tpl}
	r := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	r.Get("/settings/seo", h.form)
	r.Get("/settings/seo/audit", h.audit)
	r.Post("/settings/seo", h.save)
	d.Admin.AddNav(kernel.NavItem{ID: "settings.seo", LabelKey: "seo.nav", Icon: "search", Href: "/admin/settings/seo",
		Permission: "settings.manage", Group: "settings", Position: 710})
	return nil
}
