package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidStampAndRestoreDestination(t *testing.T) {
	if ValidStamp("bad") || ValidStamp("../x") || !ValidStamp("20261018T120000Z") {
		t.Fatal("stamp validation")
	}
	dir := t.TempDir()
	dest, err := RestoreDestination(dir, "20261018T120000Z")
	if err != nil || dest != filepath.Join(dir, "restores", "20261018T120000Z") {
		t.Fatal(dest, err)
	}
	if _, err := RestoreDestination(dir, "../escape"); err == nil {
		t.Fatal("escape stamp accepted")
	}
	if _, err := RestoreDestination(dir, "bad"); err == nil {
		t.Fatal("bad stamp accepted")
	}
}

func TestManifestFileAndRestored(t *testing.T) {
	dir := t.TempDir()
	if _, err := ManifestFile(dir, "not-a-stamp"); err == nil {
		t.Fatal("bad stamp accepted")
	}
	stamp := "20261018T120000Z"
	if err := os.MkdirAll(Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(Dir(dir), stamp+".manifest.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ManifestFile(dir, stamp)
	if err != nil || got != p {
		t.Fatal(got, err)
	}
	if path, ok := Restored(dir, stamp); ok || path != "" {
		t.Fatal("restored without database")
	}
	restoreDir := filepath.Join(dir, "restores", stamp)
	if err := os.MkdirAll(restoreDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restoreDir, "gorun.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, ok := Restored(dir, stamp); !ok || path != restoreDir {
		t.Fatal(path, ok)
	}
}
