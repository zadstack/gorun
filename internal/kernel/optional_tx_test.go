package kernel_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestAddonEnabledTransactionMatchesDatabaseBooleanSemantics(t *testing.T) {
	for _, value := range []string{"fresh-database", "missing", "true", "false", "null", `"false"`, "[]"} {
		t.Run(value, func(t *testing.T) {
			ctx := context.Background()
			database, err := db.Open(filepath.Join(t.TempDir(), "addon.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if value != "fresh-database" {
				if err := db.Migrate(ctx, database, "settings", settingsmod.New().Migrations()); err != nil {
					t.Fatal(err)
				}
				if value != "missing" {
					if _, err := database.ExecContext(ctx, `INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)`, kernel.AddonSettingKey("identitylogo"), value, db.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			fromDB, dbErr := kernel.AddonEnabled(ctx, database, "identitylogo")
			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			fromTx, txErr := kernel.AddonEnabledTx(ctx, tx, "identitylogo")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if fromDB != fromTx || (dbErr == nil) != (txErr == nil) {
				t.Fatalf("DB=%v/%v Tx=%v/%v", fromDB, dbErr, fromTx, txErr)
			}
			if dbErr != nil && dbErr.Error() != txErr.Error() {
				t.Fatalf("different activation error: %v / %v", dbErr, txErr)
			}
		})
	}
}

func TestAddonEnabledTransactionReadsItsOwnUncommittedSetting(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "addon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(ctx, database, "settings", settingsmod.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)`, kernel.AddonSettingKey("identitylogo"), "false", db.Now()); err != nil {
		t.Fatal(err)
	}
	on, err := kernel.AddonEnabledTx(ctx, tx, "identitylogo")
	if err != nil || on {
		t.Fatalf("uncommitted disable ignored: %v %v", on, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	on, err = kernel.AddonEnabled(ctx, database, "identitylogo")
	if err != nil || !on {
		t.Fatalf("rollback changed DB activation: %v %v", on, err)
	}
	if _, err := kernel.AddonEnabledTx(ctx, tx, "identitylogo"); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("closed transaction failed open: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if on, err := kernel.AddonEnabled(ctx, database, "identitylogo"); err == nil || on {
		t.Fatalf("closed database failed open: %v %v", on, err)
	}
}
