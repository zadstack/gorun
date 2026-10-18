package themekit_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/pkg/themekit"
)

func TestDoctorRootsShipWith(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	results := themekit.DoctorRoots([]string{root})
	if len(results) < len(themekit.CatalogSlugs) {
		t.Fatalf("got %d themes, want at least %d", len(results), len(themekit.CatalogSlugs))
	}
	if themekit.AnyFailed(results) {
		for _, res := range results {
			if !res.OK {
				t.Errorf("%s", themekit.FormatResult(res))
			}
		}
	}
}

func TestOptionsForSlug(t *testing.T) {
	cat := themekit.OptionsForSlug("business")
	if len(cat.RequireLocales) == 0 || !cat.RequireScreenshot {
		t.Fatalf("catalog options: %+v", cat)
	}
	custom := themekit.OptionsForSlug("acme-brand")
	if len(custom.RequireLocales) != 0 || len(custom.RequireTemplates) != 1 || custom.RequireTemplates[0] != "index.html" {
		t.Fatalf("custom options: %+v", custom)
	}
}

func TestHelpers(t *testing.T) {
	if themekit.AssetPath("docs", "theme.css") != "/themes/docs/assets/theme.css" {
		t.Fatal(themekit.AssetPath("docs", "theme.css"))
	}
	if themekit.SettingKey("business", "cta") != "theme.business.cta" {
		t.Fatal(themekit.SettingKey("business", "cta"))
	}
	if !strings.Contains(themekit.DataThemeSelector("magazine"), `data-theme="magazine"`) {
		t.Fatal(themekit.DataThemeSelector("magazine"))
	}
}

func TestLangKeyWarningsDefault(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	issues, err := themekit.LangKeyWarnings(filepath.Join(root, "default"), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	// default ships its own keys; warnings should be empty or rare.
	for _, issue := range issues {
		t.Log(issue)
	}
}
