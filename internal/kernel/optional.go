package kernel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zadstack/gorun/internal/config"
)

// OptionalModule is evaluated before any module migrations, catalog loading or
// route registration. Disabled modules retain their existing data untouched.
// Enabled must only read the database; an empty database is valid.
//
// The operator's config (config.Config.Addons, from the site config or
// GORUN_ADDONS) can only restrict: mode "disabled" turns an optional module
// off without calling Enabled; "enabled", "auto" or no entry leave the
// decision to the module, so configuration alone never proves activation.
// Modules that do not implement this interface are always booted; a mode
// configured for them is reported in the module inventory, warned about and ignored.
type OptionalModule interface {
	Enabled(context.Context, *sql.DB) (bool, error)
}

// Addon modes accepted in config.Config.Addons.
const (
	AddonModeEnabled  = "enabled"
	AddonModeDisabled = "disabled"
	AddonModeAuto     = "auto"
)

// AddonSettingKey is the settings key that records whether the optional
// module name is on: a JSON boolean under "addons.<name>.enabled". A missing
// key means enabled, so existing installs keep every module they had.
func AddonSettingKey(name string) string { return "addons." + name + ".enabled" }

// AddonEnabled is the shared OptionalModule.Enabled implementation for
// built-in optional modules: it reads AddonSettingKey(name) from the
// settings table. It runs before any module migration, so on a fresh
// database (no settings table yet) it reports enabled. A value that is not a
// JSON boolean is an activation error, which stops the boot with a message
// instead of guessing.
func AddonEnabled(ctx context.Context, d *sql.DB, name string) (bool, error) {
	var tables int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'settings'`).Scan(&tables); err != nil {
		return false, fmt.Errorf("kernel: addon %s: inspect schema: %w", name, err)
	}
	if tables == 0 {
		return true, nil
	}
	var raw string
	err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, AddonSettingKey(name)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("kernel: addon %s: read setting: %w", name, err)
	}
	// Decode into a pointer: encoding/json accepts "null" into a plain bool
	// and leaves it false, which would read as "disabled" instead of as an
	// invalid value.
	var on *bool
	if err := json.Unmarshal([]byte(raw), &on); err != nil || on == nil {
		return false, fmt.Errorf("kernel: setting %s must be a JSON boolean (true or false), got %s; repair it with GORUN_ADDONS='{\"%s\":\"disabled\"}' gorun settings set %s true", AddonSettingKey(name), raw, name, AddonSettingKey(name))
	}
	return *on, nil
}

func enabledModules(ctx context.Context, cfg config.Config, log *slog.Logger, d *sql.DB, mods []Module) ([]Module, error) {
	selected := make([]Module, 0, len(mods))
	for _, m := range mods {
		mode, configured := cfg.Addons[m.Name()]
		optional, ok := m.(OptionalModule)
		if !ok {
			if configured && log != nil {
				log.Warn("kernel: addon mode ignored: module is not optional and stays enabled", "module", m.Name(), "mode", mode)
			}
			selected = append(selected, m)
			continue
		}
		enabled := false
		if mode != AddonModeDisabled {
			var err error
			if enabled, err = optional.Enabled(ctx, d); err != nil {
				return nil, fmt.Errorf("addon %s activation: %w", m.Name(), err)
			}
		}
		if !enabled {
			if log != nil {
				log.Info("kernel: optional module disabled; its routes, workers, admin pages and hooks are not registered and its data is kept", "module", m.Name())
			}
			continue
		}
		selected = append(selected, m)
	}
	if err := validateModules(selected); err != nil {
		return nil, err
	}
	return selected, nil
}
