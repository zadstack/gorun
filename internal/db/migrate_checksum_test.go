package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

// Frozen copies of the schema_migrations SQL shipped by binaries before
// checksums existed (main 7d85132). They stand in for an old binary: do not
// update them when migrate.go changes.
const (
	oldCreateMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	module     TEXT NOT NULL,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL,
	PRIMARY KEY (module, name)
)`
	oldCheckApplied = `SELECT count(*) FROM schema_migrations WHERE module = ? AND name = ?`
	oldRecord       = `INSERT INTO schema_migrations(module, name, applied_at) VALUES (?, ?, ?)`
)

// oldMigrate replays the pre-checksum Migrate loop for one file.
func oldMigrate(t *testing.T, d *sql.DB, module, name, body string) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, oldCreateMigrationsTable); err != nil {
		t.Fatal(err)
	}
	err := InTx(ctx, d, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, oldCheckApplied, module, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, body); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, oldRecord, module, name, Now())
		return err
	})
	if err != nil {
		t.Fatalf("old binary migrate %s/%s: %v", module, name, err)
	}
}

// sum is computed independently of Checksum so a format change is caught.
func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func storedChecksum(t *testing.T, d *sql.DB, module, name string) sql.NullString {
	t.Helper()
	var c sql.NullString
	if err := d.QueryRow(`SELECT checksum FROM schema_migrations WHERE module = ? AND name = ?`, module, name).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func openTest(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateRecordsChecksum(t *testing.T) {
	d := openTest(t)
	body := "CREATE TABLE t(x);"
	rep, err := MigrateWithReport(context.Background(), d, "m", fstest.MapFS{"migrations/0001_a.sql": {Data: []byte(body)}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Module != "m" || !slices.Equal(rep.Applied, []string{"0001_a.sql"}) || len(rep.Backfilled)+len(rep.Mismatched) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if c := storedChecksum(t, d, "m", "0001_a.sql"); !c.Valid || c.String != sum(body) {
		t.Fatalf("checksum = %v, want %s", c, sum(body))
	}
}

func TestMigrateBackfillsChecksumOfLegacyRows(t *testing.T) {
	d := openTest(t)
	body := "CREATE TABLE t(x);"
	oldMigrate(t, d, "m", "0001_a.sql", body) // applied by an old binary: no checksum column yet
	rep, err := MigrateWithReport(context.Background(), d, "m", fstest.MapFS{"migrations/0001_a.sql": {Data: []byte(body)}})
	if err != nil {
		t.Fatalf("new binary on legacy table: %v", err)
	}
	if len(rep.Applied) != 0 || !slices.Equal(rep.Backfilled, []string{"0001_a.sql"}) || len(rep.Mismatched) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if c := storedChecksum(t, d, "m", "0001_a.sql"); c.String != sum(body) {
		t.Fatalf("checksum not backfilled: %v", c)
	}
	rep, err = MigrateWithReport(context.Background(), d, "m", fstest.MapFS{"migrations/0001_a.sql": {Data: []byte(body)}})
	if err != nil || len(rep.Applied)+len(rep.Backfilled)+len(rep.Mismatched) != 0 {
		t.Fatalf("second run: %+v %v", rep, err)
	}
}

func TestMigrateReportsChecksumMismatchWithoutFailing(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	orig := "CREATE TABLE t(x);"
	if err := Migrate(ctx, d, "m", fstest.MapFS{"migrations/0001_a.sql": {Data: []byte(orig)}}); err != nil {
		t.Fatal(err)
	}
	edited := fstest.MapFS{"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x, y);")}}
	rep, err := MigrateWithReport(ctx, d, "m", edited)
	if err != nil {
		t.Fatalf("a changed applied file must not fail boot: %v", err)
	}
	if !slices.Equal(rep.Mismatched, []string{"0001_a.sql"}) || len(rep.Applied) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if c := storedChecksum(t, d, "m", "0001_a.sql"); c.String != sum(orig) {
		t.Fatalf("stored checksum must keep the applied content's hash, got %v", c)
	}
}

func TestOldBinaryMigratesOnTableWithChecksumColumn(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	if err := Migrate(ctx, d, "m", fstest.MapFS{"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x);")}}); err != nil {
		t.Fatal(err)
	}
	// An old binary boots on the new schema: it re-checks 0001 and applies a
	// file of its own, leaving checksum NULL.
	oldMigrate(t, d, "m", "0001_a.sql", "CREATE TABLE t(x);")
	oldMigrate(t, d, "m", "0002_b.sql", "INSERT INTO t VALUES (1);")
	if c := storedChecksum(t, d, "m", "0002_b.sql"); c.Valid {
		t.Fatalf("old binary row should have no checksum, got %v", c)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d err = %v", n, err)
	}
	// The new binary comes back and backfills the old binary's row.
	files := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x);")},
		"migrations/0002_b.sql": {Data: []byte("INSERT INTO t VALUES (1);")},
	}
	rep, err := MigrateWithReport(ctx, d, "m", files)
	if err != nil || !slices.Equal(rep.Backfilled, []string{"0002_b.sql"}) || len(rep.Applied) != 0 {
		t.Fatalf("report = %+v err = %v", rep, err)
	}
}

func TestChecksumColumnAddIsIdempotent(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, oldCreateMigrationsTable); err != nil {
		t.Fatal(err)
	}
	// A second process may add the column between our check and ALTER:
	// running the ALTER path twice must not fail.
	for i := 0; i < 2; i++ {
		if err := addChecksumColumn(ctx, d); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := ensureMigrationsTable(ctx, d); err != nil {
			t.Fatalf("ensure %d: %v", i, err)
		}
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('schema_migrations') WHERE name = 'checksum'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("checksum columns = %d err = %v", n, err)
	}
}

func TestInspectMigrationsFindsUnknownAndChangedFiles(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	full := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x);")},
		"migrations/0002_b.sql": {Data: []byte("CREATE TABLE u(x);")},
	}
	if err := Migrate(ctx, d, "m", full); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, d, "gone", fstest.MapFS{"migrations/0001_g.sql": {Data: []byte("CREATE TABLE g(x);")}}); err != nil {
		t.Fatal(err)
	}
	oldMigrate(t, d, "legacy", "0001_l.sql", "CREATE TABLE l(x);") // no checksum: never a mismatch
	older := fstest.MapFS{"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x, edited);")}}
	got, err := InspectMigrations(ctx, d, map[string]fs.FS{
		"m":      older, // this binary only knows 0001, and with other content
		"legacy": fstest.MapFS{"migrations/0001_l.sql": {Data: []byte("changed")}},
		"empty":  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gone/0001_g.sql", "m/0002_b.sql"}; !slices.Equal(got.Unknown, want) {
		t.Fatalf("Unknown = %v, want %v", got.Unknown, want)
	}
	if want := []string{"m/0001_a.sql"}; !slices.Equal(got.Mismatched, want) {
		t.Fatalf("Mismatched = %v, want %v", got.Mismatched, want)
	}
	if !got.Ahead() {
		t.Fatal("Ahead() should be true")
	}
	clean, err := InspectMigrations(ctx, d, map[string]fs.FS{"m": full, "gone": fstest.MapFS{"migrations/0001_g.sql": {Data: []byte("CREATE TABLE g(x);")}}, "legacy": nil})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"legacy/0001_l.sql"}; !slices.Equal(clean.Unknown, want) || len(clean.Mismatched) != 0 {
		t.Fatalf("clean = %+v", clean)
	}
}
