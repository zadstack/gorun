package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMigrateAppliesOnceInOrder(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	fsys := fstest.MapFS{
		"migrations/0002_b.sql": {Data: []byte("INSERT INTO t VALUES (2);")},
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x);")},
	}
	if err := Migrate(context.Background(), d, "m", fsys); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), d, "m", fsys); err != nil {
		t.Fatal(err)
	} // idempotent
	var n int
	d.QueryRow("SELECT count(*) FROM t").Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 row got %d", n)
	}
}

func TestWALEnabled(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	var mode string
	d.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Fatal(mode)
	}
}

func TestPragmasApplied(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var fk, busy, sync int
	d.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	d.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	d.QueryRow("PRAGMA synchronous").Scan(&sync)
	if fk != 1 || busy != 5000 || sync != 1 { // synchronous NORMAL = 1
		t.Fatalf("foreign_keys=%d busy_timeout=%d synchronous=%d", fk, busy, sync)
	}
}

func TestMigrateRollsBackFailedFile(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer d.Close()
	fsys := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE t(x); INSERT INTO nope VALUES (1);")},
	}
	if err := Migrate(context.Background(), d, "m", fsys); err == nil {
		t.Fatal("want error")
	}
	var n int
	d.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='t'").Scan(&n)
	if n != 0 {
		t.Fatal("table t should have been rolled back")
	}
	d.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n)
	if n != 0 {
		t.Fatalf("want 0 recorded migrations got %d", n)
	}
}

func TestMigrateTracksPerModuleAndAcceptsNil(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer d.Close()
	ctx := context.Background()
	if err := Migrate(ctx, d, "empty", nil); err != nil {
		t.Fatal(err)
	}
	a := fstest.MapFS{"migrations/0001_x.sql": {Data: []byte("CREATE TABLE a(x);")}}
	b := fstest.MapFS{"migrations/0001_x.sql": {Data: []byte("CREATE TABLE b(x);")}}
	if err := Migrate(ctx, d, "a", a); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, d, "b", b); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n)
	if n != 2 {
		t.Fatalf("want 2 got %d", n)
	}
}

func TestMigrateRejectsBadName(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer d.Close()
	fsys := fstest.MapFS{"migrations/init.sql": {Data: []byte("CREATE TABLE t(x);")}}
	if err := Migrate(context.Background(), d, "m", fsys); err == nil {
		t.Fatal("want error for non NNNN_ name")
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer d.Close()
	ctx := context.Background()
	d.Exec("CREATE TABLE t(x)")
	err := InTx(ctx, d, func(tx *sql.Tx) error {
		tx.Exec("INSERT INTO t VALUES (1)")
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	var n int
	d.QueryRow("SELECT count(*) FROM t").Scan(&n)
	if n != 0 {
		t.Fatalf("want rollback, got %d rows", n)
	}
}
