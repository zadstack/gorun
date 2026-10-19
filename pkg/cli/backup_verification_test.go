package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

func TestBackupCLIRejectsInvalidSnapshotBeforeRetention(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "gorun.db")
	d, err := db.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE backup_parent(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	good, err := gpbackup.Create(dir, database, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	d, err = db.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	for _, sql := range []string{`PRAGMA foreign_keys=OFF`, `CREATE TABLE backup_child(parent_id TEXT REFERENCES backup_parent(id))`, `INSERT INTO backup_child VALUES ('missing')`} {
		if _, err := d.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	var out bytes.Buffer
	if err := backup([]string{"--data", dir, "--retain", "1"}, &out); err == nil {
		t.Fatal("CLI accepted an invalid snapshot")
	}
	if _, err := os.Stat(good.DB); err != nil {
		t.Fatal("CLI pruned the last good snapshot")
	}
}
