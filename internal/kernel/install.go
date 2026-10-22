package kernel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/zadstack/gorun/internal/db"
)

// Install provenance. A database that this binary creates from nothing (no
// schema_migrations ledger, or an empty one, before the first migration
// runs) is stamped once with InstallSettingKey = InstallEpoch. A database
// that already had a ledger — every upgraded site — is never stamped, so
// modules can tell a fresh install from an upgrade without inferring it
// from activity (orders, users, secrets). The epoch is bumped when the
// meaning of "fresh" changes for some module default.
const (
	InstallSettingKey = "kernel.install_epoch"
	InstallEpoch      = 1
)

// ledgerEmpty reports whether no migration has ever been recorded.
func ledgerEmpty(ctx context.Context, d *sql.DB) (bool, error) {
	var tables int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&tables); err != nil {
		return false, fmt.Errorf("kernel: inspect ledger: %w", err)
	}
	if tables == 0 {
		return true, nil
	}
	var rows int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&rows); err != nil {
		return false, fmt.Errorf("kernel: read ledger: %w", err)
	}
	return rows == 0, nil
}

// stampInstall records InstallEpoch once the settings table exists. It is a
// no-op when the key is already present.
func stampInstall(ctx context.Context, d *sql.DB) error {
	_, err := d.ExecContext(ctx, `INSERT INTO settings (key, value_json, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		InstallSettingKey, strconv.Itoa(InstallEpoch), db.Now())
	if err != nil {
		return fmt.Errorf("kernel: stamp install: %w", err)
	}
	return nil
}

// InstalledEpoch returns the install epoch stamped when this database was
// created, or 0 for a database created before the stamp existed (an
// upgraded site) — the caller must treat 0 as "existing".
func InstalledEpoch(ctx context.Context, d *sql.DB) (int, error) {
	var raw string
	err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, InstallSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("kernel: read install epoch: %w", err)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("kernel: setting %s must be a non-negative integer, got %s", InstallSettingKey, raw)
	}
	return n, nil
}
