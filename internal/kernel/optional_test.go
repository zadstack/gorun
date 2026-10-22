package kernel_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

type optionalFixture struct {
	enabled bool
	err     error
}

func (*optionalFixture) Name() string                { return "optional-fixture" }
func (*optionalFixture) Register(*kernel.Deps) error { return nil }
func (*optionalFixture) Migrations() fs.FS {
	return fstest.MapFS{"migrations/0001_optional.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE optional_fixture(id TEXT);`)}}
}
func (m *optionalFixture) Enabled(context.Context, *sql.DB) (bool, error) { return m.enabled, m.err }
func TestDisabledAddonDoesNotMigrateOrDestroyExistingData(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m := &optionalFixture{}
	mods := []kernel.Module{settingsmod.New(), m}
	if err = kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='optional_fixture'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("disabled addon migrated %v %v", n, err)
	}
	m.enabled = true
	if err = kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`INSERT INTO optional_fixture VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	m.enabled = false
	if err = kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatal(err)
	}
	if err = d.QueryRow(`SELECT count(*) FROM optional_fixture`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("disabled addon modified existing data %v %v", n, err)
	}
	m.err = errors.New("cannot read activation")
	if err = kernel.MigrateAll(ctx, d, mods); err == nil {
		t.Fatal("activation error ignored")
	}
}

// A malformed optional catalog must not affect a deployment where the addon is
// disabled. This also exercises Boot's path independently of migrate-only CLI.
func (m *optionalFixture) Catalogs() fs.FS {
	return fstest.MapFS{"catalog/optional.en.json": &fstest.MapFile{Data: []byte(`invalid json`)}}
}
func TestBootSkipsDisabledAddonCatalogAndMigration(t *testing.T) {
	cfg := testConfig(t)
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), &optionalFixture{}}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var n int
	if err = app.Deps.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='optional_fixture'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("disabled addon migrated: %d %v", n, err)
	}
}

// Operator config can only restrict: mode "disabled" never calls Enabled;
// "enabled", "auto" and no entry leave the decision to the module. A mode
// for a module that is not optional is ignored (the module boots).
func TestAddonConfigModeDisabledWinsAndOtherModesAskTheModule(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m := &optionalFixture{enabled: false, err: errors.New("Enabled must not be consulted")}
	cfg := config.Config{Addons: map[string]string{"optional-fixture": kernel.AddonModeDisabled, "settings": kernel.AddonModeDisabled}}
	if err := kernel.MigrateAllWithConfig(ctx, cfg, d, []kernel.Module{settingsmod.New(), m}); err != nil {
		t.Fatalf("mode disabled: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='optional_fixture'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("disabled by config still migrated: %d %v", n, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='settings'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("non-optional settings module must boot despite mode disabled: %d %v", n, err)
	}
	for _, mode := range []string{kernel.AddonModeEnabled, kernel.AddonModeAuto, ""} {
		cfg := config.Config{Addons: map[string]string{"optional-fixture": mode}}
		if err := kernel.MigrateAllWithConfig(ctx, cfg, d, []kernel.Module{settingsmod.New(), m}); err == nil || !strings.Contains(err.Error(), "Enabled must not be consulted") {
			t.Fatalf("mode %q must ask the module: %v", mode, err)
		}
	}
	m.err, m.enabled = nil, true
	cfg = config.Config{Addons: map[string]string{"optional-fixture": kernel.AddonModeEnabled}}
	if err := kernel.MigrateAllWithConfig(ctx, cfg, d, []kernel.Module{settingsmod.New(), m}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='optional_fixture'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("module enabled itself but was not migrated: %d %v", n, err)
	}
}

// AddonEnabled: enabled on a fresh database and when the key is missing,
// follows the JSON boolean when set, and refuses a non-boolean value.
func TestAddonEnabledReadsSetting(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if on, err := kernel.AddonEnabled(ctx, d, "commerce"); err != nil || !on {
		t.Fatalf("fresh database: on=%v err=%v", on, err)
	}
	if err := kernel.MigrateAll(ctx, d, []kernel.Module{settingsmod.New()}); err != nil {
		t.Fatal(err)
	}
	if on, err := kernel.AddonEnabled(ctx, d, "commerce"); err != nil || !on {
		t.Fatalf("missing key: on=%v err=%v", on, err)
	}
	st := settings.New(d)
	if err := st.Set(ctx, kernel.AddonSettingKey("commerce"), false); err != nil {
		t.Fatal(err)
	}
	if on, err := kernel.AddonEnabled(ctx, d, "commerce"); err != nil || on {
		t.Fatalf("false setting: on=%v err=%v", on, err)
	}
	if err := st.Set(ctx, kernel.AddonSettingKey("commerce"), true); err != nil {
		t.Fatal(err)
	}
	if on, err := kernel.AddonEnabled(ctx, d, "commerce"); err != nil || !on {
		t.Fatalf("true setting: on=%v err=%v", on, err)
	}
	if err := st.Set(ctx, kernel.AddonSettingKey("commerce"), "yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.AddonEnabled(ctx, d, "commerce"); err == nil || !strings.Contains(err.Error(), "JSON boolean") {
		t.Fatalf("non-boolean must fail loudly: %v", err)
	}
}
