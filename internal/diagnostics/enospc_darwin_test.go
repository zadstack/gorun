package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func TestCollectorFilesystemFullInOwnedDiskImage(t *testing.T) {
	root := os.Getenv("AUDIT_DIAGNOSTICS_FS_ROOT")
	if root == "" {
		t.Skip("requires an explicitly mounted bounded owned filesystem fixture")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".gorun-audit-owned"))
	if err != nil || string(marker) != "diagnostics-owned-bounded-fixture" {
		t.Fatal("filesystem fixture ownership marker missing")
	}
	var stats syscall.Statfs_t
	if err := syscall.Statfs(root, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Blocks*uint64(stats.Bsize) > 32<<20 {
		t.Fatal("refusing to fill a filesystem larger than 32MiB")
	}
	dir, err := os.MkdirTemp(root, "diagnostics-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error("owned fixture cleanup failed", err)
		}
	})
	d, err := db.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d, "diagnostics", Migrations()); err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	c := New(d)
	t.Cleanup(func() {
		c.Close()
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	c.observe("http", "http_5xx", 500, time.Now())
	c.flush(false)
	first := c.LastSuccessfulWrite()
	if first.IsZero() {
		t.Fatal("initial write failed")
	}
	var busy, logged, checkpointed int
	if err := d.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logged, &checkpointed); err != nil || busy != 0 {
		t.Fatal("checkpoint failed", err, busy)
	}
	fill, err := os.Create(filepath.Join(dir, "fill.bin"))
	if err != nil {
		t.Fatal(err)
	}
	block := make([]byte, int(stats.Bsize))
	full := false
	for total := 0; total < 32<<20; total += len(block) {
		_, err := fill.Write(block)
		if errors.Is(err, syscall.ENOSPC) {
			full = true
			break
		}
		if err != nil {
			fill.Close()
			t.Fatal("unexpected filler error", err)
		}
	}
	syncErr, closeErr := fill.Sync(), fill.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.ENOSPC) {
		t.Fatal(syncErr)
	}
	if closeErr != nil && !errors.Is(closeErr, syscall.ENOSPC) {
		t.Fatal(closeErr)
	}
	if !full {
		t.Fatal("bounded filesystem did not return ENOSPC")
	}
	for i := 0; i < 100; i++ {
		c.observe("http", "http_5xx", 500+i, time.Now())
	}
	c.flush(false)
	if c.StorageFailures() == 0 || c.Dropped() != 100 {
		t.Fatal("filesystem-full failure was not counted", c.StorageFailures(), c.Dropped())
	}
	if !c.LastSuccessfulWrite().Equal(first) {
		t.Fatal("ENOSPC advanced successful write time")
	}
}
