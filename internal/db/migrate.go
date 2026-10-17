package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
)

// migrationName enforces "NNNN_description.sql".
var migrationName = regexp.MustCompile(`^[0-9]{4}_[A-Za-z0-9_]+\.sql$`)

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	module     TEXT NOT NULL,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL,
	PRIMARY KEY (module, name)
)`

// MigrateReport is what one module's migrate run did. Names are file names
// ("0001_x.sql").
type MigrateReport struct {
	Module     string
	Applied    []string // applied by this run
	Backfilled []string // applied earlier without a checksum; checksum recorded now
	Mismatched []string // applied earlier, but the file in this binary has different content
}

// Migrate applies the module's pending migrations from files. Files must live
// at "migrations/NNNN_name.sql" inside files; they are applied in lexical order,
// each in its own transaction, and recorded in schema_migrations(module, name,
// applied_at, checksum). A nil FS or one without migrations is a no-op.
func Migrate(ctx context.Context, d *sql.DB, module string, files fs.FS) error {
	_, err := MigrateWithReport(ctx, d, module, files)
	return err
}

// MigrateWithReport is Migrate, also reporting what it applied and which
// already-applied files no longer match their recorded sha256 checksum. A
// mismatch is not an error: the caller decides how loudly to warn.
func MigrateWithReport(ctx context.Context, d *sql.DB, module string, files fs.FS) (MigrateReport, error) {
	rep := MigrateReport{Module: module}
	if err := ensureMigrationsTable(ctx, d); err != nil {
		return rep, fmt.Errorf("migrate %s: %w", module, err)
	}
	if files == nil {
		return rep, nil
	}
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return rep, fmt.Errorf("migrate %s: list: %w", module, err)
	}
	sort.Strings(names)
	for _, p := range names {
		name := path.Base(p)
		if !migrationName.MatchString(name) {
			return rep, fmt.Errorf("migrate %s: bad migration name %q (want NNNN_name.sql)", module, name)
		}
		body, err := fs.ReadFile(files, p)
		if err != nil {
			return rep, fmt.Errorf("migrate %s: read %s: %w", module, name, err)
		}
		outcome, err := applyOne(ctx, d, module, name, string(body))
		if err != nil {
			return rep, err
		}
		switch outcome {
		case outcomeApplied:
			rep.Applied = append(rep.Applied, name)
		case outcomeBackfilled:
			rep.Backfilled = append(rep.Backfilled, name)
		case outcomeMismatch:
			rep.Mismatched = append(rep.Mismatched, name)
		}
	}
	return rep, nil
}

// Checksum is the hex sha256 recorded for a migration file's content.
func Checksum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// ensureMigrationsTable creates schema_migrations and adds the nullable
// checksum column once. Binaries from before checksums ignore the column:
// they insert with an explicit column list, leaving it NULL.
func ensureMigrationsTable(ctx context.Context, d *sql.DB) error {
	if _, err := d.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	ok, err := hasChecksumColumn(ctx, d)
	if err != nil || ok {
		return err
	}
	return addChecksumColumn(ctx, d)
}

func hasChecksumColumn(ctx context.Context, d *sql.DB) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('schema_migrations') WHERE name = 'checksum'`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("inspect schema_migrations: %w", err)
	}
	return n > 0, nil
}

// addChecksumColumn adds the column. Another process may win the race to
// add it; the ALTER then fails, and the column being there is success.
func addChecksumColumn(ctx context.Context, d *sql.DB) error {
	_, err := d.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN checksum TEXT`)
	if err == nil {
		return nil
	}
	if ok, cerr := hasChecksumColumn(ctx, d); cerr == nil && ok {
		return nil
	}
	return fmt.Errorf("add schema_migrations.checksum: %w", err)
}

type outcome int

const (
	outcomeUnchanged outcome = iota
	outcomeApplied
	outcomeBackfilled
	outcomeMismatch
)

func applyOne(ctx context.Context, d *sql.DB, module, name, body string) (outcome, error) {
	sum := Checksum(body)
	var out outcome
	err := InTx(ctx, d, func(tx *sql.Tx) error {
		var stored sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT checksum FROM schema_migrations WHERE module = ? AND name = ?`, module, name).Scan(&stored)
		switch {
		case err == nil && !stored.Valid:
			if _, err := tx.ExecContext(ctx,
				`UPDATE schema_migrations SET checksum = ? WHERE module = ? AND name = ? AND checksum IS NULL`, sum, module, name); err != nil {
				return fmt.Errorf("migrate %s: backfill checksum %s: %w", module, name, err)
			}
			out = outcomeBackfilled
			return nil
		case err == nil:
			if stored.String != sum {
				out = outcomeMismatch
			}
			return nil
		case err != sql.ErrNoRows:
			return fmt.Errorf("migrate %s: check %s: %w", module, name, err)
		}
		if _, err := tx.ExecContext(ctx, body); err != nil {
			return fmt.Errorf("migrate %s: apply %s: %w", module, name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(module, name, applied_at, checksum) VALUES (?, ?, ?, ?)`, module, name, Now(), sum); err != nil {
			return fmt.Errorf("migrate %s: record %s: %w", module, name, err)
		}
		out = outcomeApplied
		return nil
	})
	if err != nil {
		return outcomeUnchanged, err
	}
	return out, nil
}
