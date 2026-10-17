// Package settingsmod is the settings kernel.Module. It lives apart from
// package settings because kernel.Deps references *settings.Store, so
// package settings cannot import kernel. The store itself is constructed by
// kernel.Boot; this module owns the migrations and the permission.
package settingsmod

import (
	"io/fs"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// Module is the settings module.
type Module struct{}

// New returns the settings module.
func New() Module { return Module{} }

// Name implements kernel.Module.
func (Module) Name() string { return "settings" }

// Migrations implements kernel.Module.
func (Module) Migrations() fs.FS { return settings.Migrations() }

// Register implements kernel.Module.
func (Module) Register(d *kernel.Deps) error {
	d.Perms.Declare("settings.manage", map[string]string{"en": "Manage settings", "ar": "إدارة الإعدادات"})
	return nil
}
