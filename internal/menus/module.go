package menus

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"reflect"
	"slices"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the menus kernel.Module.
type Module struct{}

// New returns the menus module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "menus" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes kernel.ServiceMenus and adds the admin editor (under
// Appearance) and the API operations. The content, taxonomy and theme
// services are looked up per call: they may register later or be absent.
func (*Module) Register(d *kernel.Deps) error {
	svc := &service{db: d.DB, hooks: d.Hooks, audit: d.Audit, log: d.Log,
		content:  func() (content.Service, bool) { return kernel.Get[content.Service](d, kernel.ServiceContent) },
		taxonomy: func() (taxonomy.Service, bool) { return kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy) },
	}
	d.Services.Set(kernel.ServiceMenus, Service(svc))

	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("menus: admin templates not registered (auth module must boot first)")
	}
	h := &adminHandlers{svc: svc, tpl: tpl, log: d.Log, locations: func(ctx context.Context) []string { return themeLocations(ctx, d) }}
	r := d.Router.Admin.With(auth.RequirePerm("menus.manage"))
	r.Get("/menus", h.list)
	r.Post("/menus", h.create)
	r.Get("/menus/{id}", h.edit)
	r.Post("/menus/{id}", h.update)
	r.Post("/menus/{id}/items", h.saveItems)
	r.Post("/menus/{id}/delete", h.remove)
	r.Get("/x/menus/search", h.search)
	d.Admin.AddNav(kernel.NavItem{ID: "menus", LabelKey: "menus.nav", Icon: "list-tree", Href: "/admin/menus",
		Permission: "menus.manage", Group: "appearance", Position: 20})
	registerAPI(d, svc)
	return nil
}

// themeLocations returns the menu locations declared by the active theme
// (theme.Info.Locations), or DefaultLocations. The theme module boots after
// menus and menus may not import it, so the service is reached by name and
// read by reflection: Active(context.Context) returning a struct with a
// Locations []string field.
func themeLocations(ctx context.Context, d *kernel.Deps) []string {
	v, ok := kernel.Get[any](d, kernel.ServiceTheme)
	if !ok || v == nil {
		return slices.Clone(DefaultLocations)
	}
	m := reflect.ValueOf(v).MethodByName("Active")
	if !m.IsValid() || m.Type().NumIn() != 1 || !reflect.TypeOf(ctx).AssignableTo(m.Type().In(0)) || m.Type().NumOut() < 1 {
		return slices.Clone(DefaultLocations)
	}
	out := reflect.Indirect(m.Call([]reflect.Value{reflect.ValueOf(ctx)})[0])
	if out.Kind() != reflect.Struct {
		return slices.Clone(DefaultLocations)
	}
	f := out.FieldByName("Locations")
	if !f.IsValid() || f.Kind() != reflect.Slice || f.Type().Elem().Kind() != reflect.String || f.Len() == 0 {
		return slices.Clone(DefaultLocations)
	}
	locs := make([]string, 0, f.Len())
	for i := range f.Len() {
		if s := f.Index(i).String(); slugPattern.MatchString(s) {
			locs = append(locs, s)
		}
	}
	if len(locs) == 0 {
		return slices.Clone(DefaultLocations)
	}
	return locs
}
