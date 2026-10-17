package builder

import (
	"github.com/zadstack/gorun/internal/brand"
	"strings"
	"testing"
)

func TestStarterDesignsAreCompleteAndHaveDistinctSafeIdentities(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range []string{"company", "portfolio", "magazine"} {
		design := starterDesign(kind)
		if design.Theme == "" || seen[design.Theme] {
			t.Fatalf("missing distinct theme: %+v", design)
		}
		seen[design.Theme] = true
		if err := brand.Validate(design.Identity); err != nil {
			t.Fatal(err)
		}
		for _, locale := range []string{"ar", "en"} {
			pages := starterPages(kind, "Studio", "Introduction", locale)
			if len(pages) < 4 {
				t.Fatalf("incomplete %s: %d pages", kind, len(pages))
			}
			for _, page := range pages {
				if len(page.Document.Sections) < 2 {
					t.Fatalf("bare page %s/%s", kind, page.Slug)
				}
				for i := range page.Document.Sections {
					section := &page.Document.Sections[i]
					section.Link = strings.ReplaceAll(section.Link, "@", "/")
					for j := range section.Items {
						section.Items[j].Link = strings.ReplaceAll(section.Items[j].Link, "@", "/")
					}
				}
				if err := ValidatePublication(page.Document); err != nil {
					t.Fatal(err)
				}
			}
			imageCount := 0
			for _, page := range pages {
				for _, section := range page.Document.Sections {
					if section.Image != "" {
						imageCount++
					}
					for _, entry := range section.Items {
						if entry.Image != "" {
							imageCount++
						}
					}
				}
			}
			if imageCount < 2 {
				t.Fatalf("no designed imagery in %s", kind)
			}
		}
	}
}
