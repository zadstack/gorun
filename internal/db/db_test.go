package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenEscapesSpecialCharactersInPath(t *testing.T) {
	for _, name := range []string{"plain", "with space", "q?mark", "hash#tag", "pct%41", "all ?#% mixed"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			path := filepath.Join(dir, "gorun.db")
			d, err := Open(path)
			if err != nil {
				t.Fatalf("open %q: %v", path, err)
			}
			defer d.Close()
			if _, err := d.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
				t.Fatalf("database not created at the exact path %q: %v", path, err)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 1 || entries[0].Name() != name {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("stray files next to the data directory: %v", names)
			}
			// Several connections at once: every pooled connection must get the pragmas.
			tx1, err := d.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx1.Rollback()
			var mode string
			var fk, busy int
			if err := d.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
				t.Fatalf("journal_mode = %q, %v; want wal", mode, err)
			}
			if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
				t.Fatalf("foreign_keys = %d, %v; want 1", fk, err)
			}
			if err := d.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil || busy != 5000 {
				t.Fatalf("busy_timeout = %d, %v; want 5000", busy, err)
			}
		})
	}
}

func TestDSNPathPercentEncodesReservedCharacters(t *testing.T) {
	got, err := FileURI("/srv/a b/q?x/h#y/p%z/gorun.db")
	if err != nil {
		t.Fatal(err)
	}
	if got != "file:///srv/a%20b/q%3Fx/h%23y/p%25z/gorun.db" {
		t.Fatalf("FileURI = %q", got)
	}
	rel, err := FileURI("data/gorun.db")
	if err != nil || !strings.HasPrefix(rel, "file:///") || !strings.HasSuffix(rel, "/data/gorun.db") {
		t.Fatalf("relative path must become absolute: %q %v", rel, err)
	}
}
