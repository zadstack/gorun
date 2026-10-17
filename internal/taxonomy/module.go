package taxonomy

import (
	"embed"
	"io/fs"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the taxonomy kernel.Module.
type Module struct{}

// New returns the taxonomy module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "taxonomy" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes taxonomy.Service (kernel.ServiceTaxonomy; content
// resolves it as its term backend), declares taxonomy.manage and mounts the
// admin screens, editor picker fragments and API operations.
func (*Module) Register(d *kernel.Deps) error {
	svc := newService(d.DB, d.Hooks, d.Audit, func() content.Service {
		cs, _ := kernel.Get[content.Service](d, kernel.ServiceContent)
		return cs
	})
	d.Services.Set(kernel.ServiceTaxonomy, Service(svc))
	d.Perms.Declare("taxonomy.manage", map[string]string{"en": "Manage taxonomies", "ar": "إدارة التصنيفات"})
	registerAdmin(d, svc)
	registerAPI(d, svc)
	return nil
}
