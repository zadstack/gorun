package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

func TestBackupRestoreNewDirectoryAndRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gorun.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE fixture(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err := os.Mkdir(filepath.Join(dir, "media"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "media", "a.txt"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := gpbackup.Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored-site")
	var out bytes.Buffer
	if err := backupRestore([]string{"--input", res.Manifest, "--dest", dest}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"readiness": "local_ok"`) || !strings.Contains(out.String(), "restored: "+dest) {
		t.Fatal(out.String())
	}
	if data, err := os.ReadFile(filepath.Join(dest, "media", "a.txt")); err != nil || string(data) != "media" {
		t.Fatal("media not restored", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "secret.key")); !os.IsNotExist(err) {
		t.Fatal("default backup must not restore secret.key")
	}
	out.Reset()
	if err := backupRestore([]string{"--input", res.Manifest, "--dest", dest}, &out); err == nil {
		t.Fatal("existing destination accepted")
	}
	if err := backupRestore([]string{"--input", res.Manifest}, &out); err == nil {
		t.Fatal("missing --dest accepted")
	}
	if err := os.Remove(res.Files); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	out.Reset()
	if err := backupRestore([]string{"--input", res.Manifest, "--dest", blocked}, &out); err == nil {
		t.Fatal("blocked pair restored")
	}
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatal("blocked restore left destination")
	}
}
