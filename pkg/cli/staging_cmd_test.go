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

func seedSourceData(t *testing.T) (dir, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	dbPath = filepath.Join(dir, "gorun.db")
	d, err := db.Open(dbPath)
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
	return dir, dbPath
}

func TestStagingSeedFromCreatesDest(t *testing.T) {
	from, _ := seedSourceData(t)
	dest := filepath.Join(t.TempDir(), "staging-site")
	var out bytes.Buffer
	if err := stagingCmd([]string{"seed", "--from", from, "--dest", dest}, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "restored: "+dest) || !strings.Contains(s, "Serve staging") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "docs/STAGING.md") {
		t.Fatal(s)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "media", "a.txt")); err != nil || string(data) != "media" {
		t.Fatal("media not restored", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "secret.key")); !os.IsNotExist(err) {
		t.Fatal("default seed must not restore secret.key")
	}
}

func TestStagingPromoteFromPrintsCutover(t *testing.T) {
	from, _ := seedSourceData(t)
	dest := filepath.Join(t.TempDir(), "live-new")
	var out bytes.Buffer
	if err := stagingCmd([]string{"promote", "--from", from, "--dest", dest}, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "Stop every process") || !strings.Contains(s, "Point the live service") {
		t.Fatal(s)
	}
	if strings.Contains(s, "Serve staging") {
		t.Fatal("promote used seed messaging")
	}
}

func TestStagingSeedInputAndRefusesExisting(t *testing.T) {
	from, dbPath := seedSourceData(t)
	res, err := gpbackup.Create(from, dbPath, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "from-manifest")
	var out bytes.Buffer
	if err := stagingCmd([]string{"seed", "--input", res.Manifest, "--dest", dest}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := stagingCmd([]string{"seed", "--input", res.Manifest, "--dest", dest}, &out); err == nil {
		t.Fatal("existing destination accepted")
	}
}

func TestStagingFlagValidation(t *testing.T) {
	var out bytes.Buffer
	cases := [][]string{
		{},
		{"seed"},
		{"seed", "--dest", "/tmp/x"},
		{"seed", "--from", "/tmp/a", "--input", "/tmp/b", "--dest", "/tmp/c"},
		{"seed", "--input", "/tmp/m", "--dest", "/tmp/c", "--include-secrets"},
		{"other", "--from", "/tmp/a", "--dest", "/tmp/c"},
	}
	for _, args := range cases {
		if err := stagingCmd(args, &out); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}
