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

func TestBackupInspectReadOnlyAndBlockedExit(t *testing.T) {
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
	res, err := gpbackup.Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := backupInspect([]string{"--input", res.Manifest}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"readiness": "local_ok"`) || !strings.Contains(out.String(), "independent_original_secret_required") || !strings.Contains(out.String(), `"cutover_checklist"`) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.key")); !os.IsNotExist(err) {
		t.Fatal("inspection generated a secret")
	}
	if err := os.Remove(res.Files); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := backupInspect([]string{"--input", res.Manifest}, &out); err == nil || !strings.Contains(out.String(), `"readiness": "blocked"`) {
		t.Fatal("missing pair not blocked")
	}
	missing := filepath.Join(dir, "does-not-exist", res.Stamp+".manifest.json")
	if err := backupInspect([]string{"--input", missing}, &out); err == nil {
		t.Fatal("missing manifest accepted")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("inspection created source directory")
	}
}
