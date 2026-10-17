package kernel_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
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
