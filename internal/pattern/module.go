// Package pattern is the site-layout pattern system: declarative packages
// that layer opinionated page templates above the active theme identity.
// Themes stay type/color/spacing; patterns are magazine/company/portfolio-
// style layouts selectable independently. See docs/PATTERN-PLUGINS.md.
package pattern

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the pattern kernel.Module.
type Module struct {
	root string
	svc  *service
}

// Option configures the module.
type Option func(*Module)

// WithPatternsDir sets the ship-with patterns directory.
func WithPatternsDir(dir string) Option { return func(m *Module) { m.root = dir } }

// New returns the pattern module.
func New(opts ...Option) *Module {
	m := &Module{}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Name implements kernel.Module.
func (*Module) Name() string { return "pattern" }

// Migrations implements kernel.Module (settings key only).
func (*Module) Migrations() fs.FS { return nil }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes the service, wires the theme pattern layer, admin UI
// and public pattern assets.
func (m *Module) Register(d *kernel.Deps) error {
	root := m.root
	if root == "" {
		root = PatternsDir(d.Config)
	}
	// Soft-require: empty dir is fine (operators may only use DataDir packages).
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		if abs, e := filepath.Abs(root); e == nil {
			root = abs
		}
		d.Log.Info("pattern: ship-with directory missing; data-dir packages only", "dir", root)
	}

	th, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme)
	if !ok {
		return errors.New("pattern: theme service missing (theme module must boot first)")
	}
	aware, ok := th.(theme.PatternAware)
	if !ok {
		return errors.New("pattern: theme service does not support pattern layers")
	}

	m.svc = newService(d, root, aware)
	aware.SetPatternLayer(m.svc)
	d.Services.Set(kernel.ServicePattern, Service(m.svc))

	// Warn at boot when the stored pattern is missing (do not fail boot).
	if slug := m.svc.activeSlug(context.Background()); slug != "" {
		if _, err := m.svc.Lookup(slug); err != nil {
			d.Log.Warn("pattern: active pattern not found", "slug", slug, "err", err)
		}
	}

	if err := registerAdmin(d, m.svc); err != nil {
		return fmt.Errorf("pattern: admin: %w", err)
	}
	registerPublicAssets(d, m.svc)
	return nil
}
