package theme

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
)

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the theme kernel.Module: it publishes theme.Service, the
// menus.resolve_url filter, the Appearance admin screens and the
// themes/templates API operations. Templates are parsed lazily on first
// render (after every module has added its funcs).
type Module struct {
	root string
	log  *slog.Logger
	svc  *service
}

// Option configures the module.
type Option func(*Module)

// WithThemesDir sets the themes directory (default: ThemesDir(cfg)).
func WithThemesDir(dir string) Option { return func(m *Module) { m.root = dir } }

// WithLogger sets the logger for render errors (default: Deps.Log).
func WithLogger(l *slog.Logger) Option { return func(m *Module) { m.log = l } }

// New returns the theme module.
func New(opts ...Option) *Module {
	m := &Module{}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Name implements kernel.Module.
func (*Module) Name() string { return "theme" }

// Migrations implements kernel.Module (overrides live on disk).
func (*Module) Migrations() fs.FS { return nil }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes the service and wires hooks, admin and API.
func (m *Module) Register(d *kernel.Deps) error {
	root := m.root
	if root == "" {
		root = ThemesDir(d.Config)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		where := root
		if abs, err := filepath.Abs(root); err == nil {
			where = abs
		}
		return fmt.Errorf("themes directory not found (looked in %s); set %s or run from the repo root", where, EnvThemesDir)
	}
	m.svc = newService(d, root, m.log)
	m.svc.extra = ExtraThemesDirs()
	if err := m.svc.migrateRetiredTheme(context.Background()); err != nil {
		return fmt.Errorf("theme: migrate retired theme: %w", err)
	}
	// Fail at boot, not with a 500 on every public page.
	if slug := m.svc.activeSlug(context.Background()); slug != "" {
		if _, err := findInfo(m.svc.roots(), slug); err != nil {
			return fmt.Errorf("active theme %q not found in %s (%v); restore it there, or point %s at a themes directory that has it", slug, root, err, EnvThemesDir)
		}
	}
	d.Services.Set(kernel.ServiceTheme, Service(m.svc))
	d.Hooks.Filter(HookMenuResolveURL, 10, m.svc.links.resolveMenuURL)
	d.Hooks.Filter(seo.FilterSitemapEntry, 10, m.svc.links.sitemapEntry)
	d.Perms.Declare(PermManage, map[string]string{"en": "Manage themes", "ar": "إدارة القوالب"})
	registerAdmin(d, m.svc)
	registerAPI(d, m.svc)
	return nil
}
