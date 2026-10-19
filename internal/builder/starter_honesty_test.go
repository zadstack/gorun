package builder

import (
	"strings"
	"testing"
	"unicode"
)

// Brief §5: starters must not invent testimonials/clients or ship lorem / em dashes.
func TestCompanyStarterOmitsTestimonials(t *testing.T) {
	for _, locale := range []string{"ar", "en"} {
		pages := starterPages("company", "Studio", "Tagline", locale)
		for _, page := range pages {
			for _, sec := range page.Document.Sections {
				if sec.Kind == "testimonials" {
					t.Fatalf("company starter must omit testimonials (got on %s/%s)", locale, page.Slug)
				}
				for _, item := range sec.Items {
					blob := item.Title + " " + item.Text
					if strings.Contains(blob, "[Client") || strings.Contains(blob, "[اسم العميل") {
						t.Fatalf("invented client placeholder on %s/%s: %q", locale, page.Slug, blob)
					}
				}
			}
		}
	}
}

func TestStarterCopyHasNoLoremOrEmDash(t *testing.T) {
	for _, kind := range []string{"company", "portfolio", "magazine"} {
		for _, locale := range []string{"ar", "en"} {
			pages := starterPages(kind, "Studio", "Introduction", locale)
			for _, page := range pages {
				check := func(s, where string) {
					t.Helper()
					lower := strings.ToLower(s)
					if strings.Contains(lower, "lorem") || strings.Contains(lower, "ipsum") {
						t.Fatalf("%s %s/%s: lorem in %s: %q", kind, locale, page.Slug, where, s)
					}
					if strings.ContainsRune(s, '\u2014') {
						t.Fatalf("%s %s/%s: em dash in %s: %q", kind, locale, page.Slug, where, s)
					}
				}
				check(page.Title, "title")
				for _, sec := range page.Document.Sections {
					check(sec.Title, "section title")
					check(sec.Text, "section text")
					for _, item := range sec.Items {
						check(item.Title, "item title")
						check(item.Text, "item text")
					}
				}
			}
		}
	}
}

func TestStarterCopyIsPrintable(t *testing.T) {
	// Sanity: generated strings are not empty control-only junk.
	pages := starterPages("company", "Studio", "Introduction", "en")
	home := pages[0]
	if home.Slug != "home" || len(home.Document.Sections) < 3 {
		t.Fatalf("unexpected home: %+v", home)
	}
	for _, sec := range home.Document.Sections {
		for _, r := range sec.Title {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				t.Fatalf("control rune in title %q", sec.Title)
			}
		}
	}
}
