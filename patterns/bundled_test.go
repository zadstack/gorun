package patterns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "bundled-patterns")
	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"company", "editorial", "guidebook", "showcase"} {
		if _, err := os.Stat(filepath.Join(dir, slug, "pattern.json")); err != nil {
			t.Errorf("%s: %v", slug, err)
		}
	}
	// A current copy is left alone.
	marker := filepath.Join(dir, "company", "marker")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a current copy must not be rewritten: %v", err)
	}
	// A copy from another release is replaced.
	if err := os.WriteFile(filepath.Join(dir, stampFile), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a stale copy must be replaced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "company", "pattern.json")); err != nil {
		t.Fatal(err)
	}
}
