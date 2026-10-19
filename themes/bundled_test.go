package themes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallDefaultPreservesEdits(t *testing.T) {
	root := t.TempDir()
	if err := InstallDefault(root); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "default", "theme.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("customized"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallDefault(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(manifest)
	if err != nil || string(b) != "customized" {
		t.Fatalf("theme overwritten: %v", err)
	}
}
