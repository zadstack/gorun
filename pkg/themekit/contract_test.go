package themekit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/pkg/themekit"
)

func TestValidateCatalogRootShipWith(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	results := themekit.ValidateCatalogRoot(root)
	if len(results) != len(themekit.CatalogSlugs) {
		t.Fatalf("got %d results, want %d", len(results), len(themekit.CatalogSlugs))
	}
	for _, res := range results {
		if !res.OK {
			t.Errorf("%s: %v", res.Slug, res.Issues)
		}
	}
}

func TestValidateDirReportsMissingLocale(t *testing.T) {
	dir := t.TempDir()
	slug := "child"
	themeDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(filepath.Join(themeDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := "default"
	raw, _ := json.Marshal(map[string]any{
		"name": "Child", "version": "1.0.0", "parent": parent, "supports": []string{"rtl", "identity"},
	})
	if err := os.WriteFile(filepath.Join(themeDir, "theme.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "templates", "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point roots at real themes so parent resolves, but child still lacks lang.
	themes, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	res := themekit.ValidateDir(themeDir, []string{themes}, themekit.CatalogOptions())
	if res.OK {
		t.Fatal("expected failure without lang files")
	}
	var sawLocale bool
	for _, issue := range res.Issues {
		if issue.Code == "locale" {
			sawLocale = true
			break
		}
	}
	if !sawLocale {
		t.Fatalf("expected locale issue, got %v", res.Issues)
	}
}
