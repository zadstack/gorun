package theme

import (
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/pkg/themekit"
)

// TestShipWithCatalogMeetsThemekitContract keeps the catalog on the shared
// contract owned by pkg/themekit. When theme doctor lands, it should call the
// same ValidateDir / ValidateCatalogRoot helpers.
func TestShipWithCatalogMeetsThemekitContract(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range themekit.ValidateCatalogRoot(root) {
		if !res.OK {
			t.Errorf("%s failed catalog contract:", res.Slug)
			for _, issue := range res.Issues {
				t.Errorf("  - %s", issue)
			}
		}
	}
}

// TestShipWithCatalogRendersHomeAndSearch is a cheap smoke: activate each
// catalog theme and render home + search in ar and en without a 500.
func TestShipWithCatalogRendersHomeAndSearch(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	e := bootRealTheme(t, root)
	for _, slug := range themekit.CatalogSlugs {
		if err := e.svc.Activate(t.Context(), slug); err != nil {
			t.Fatalf("activate %s: %v", slug, err)
		}
		for _, locale := range []string{"ar", "en"} {
			for _, kind := range []string{KindHome, KindSearch} {
				rec, err := e.render(kernelKind(kind), &Context{Locale: locale, Query: map[string]string{"q": "x"}})
				if err != nil {
					t.Fatalf("%s %s %s: %v", slug, locale, kind, err)
				}
				if rec.Code >= 500 {
					t.Fatalf("%s %s %s: status %d", slug, locale, kind, rec.Code)
				}
				body := rec.Body.String()
				mustContain(t, body, `data-theme="`+slug+`"`, `lang="`+locale+`"`, "</html>")
				mustNotContain(t, body, Marker500, "cdn.tailwindcss.com", "cdn.jsdelivr.net")
			}
		}
	}
}

func kernelKind(kind string) kernel.KindRequest {
	return kernel.KindRequest{Kind: kind}
}
