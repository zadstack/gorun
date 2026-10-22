package kernel_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// A database created from nothing is stamped with the install epoch; one
// that already had a migration ledger (an upgrade from a binary without the
// stamp) never is, however many times it migrates afterwards.
func TestInstallEpochStampsOnlyFreshDatabases(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mods := []kernel.Module{settingsmod.New()}
	if err := kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatal(err)
	}
	if n, err := kernel.InstalledEpoch(ctx, d); err != nil || n != kernel.InstallEpoch {
		t.Fatalf("fresh database epoch = %d, %v", n, err)
	}
	// Simulate a database from before the stamp: ledger present, no stamp.
	if _, err := d.Exec(`DELETE FROM settings WHERE key = ?`, kernel.InstallSettingKey); err != nil {
		t.Fatal(err)
	}
	if err := kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatal(err)
	}
	if n, err := kernel.InstalledEpoch(ctx, d); err != nil || n != 0 {
		t.Fatalf("upgraded database must stay unstamped, got %d, %v", n, err)
	}
	// Boot stamps a fresh data directory the same way.
	app, err := kernel.Boot(ctx, testConfig(t), mods)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if n, err := kernel.InstalledEpoch(ctx, app.Deps.DB); err != nil || n != kernel.InstallEpoch {
		t.Fatalf("booted fresh database epoch = %d, %v", n, err)
	}
}
