package content

import (
	"strings"
	"testing"
)

// TestSanitizeKeepsLocalizedImageAlt guards localized alt text through the
// content sanitizer (the native save path). bluemonday's UGC base matches img
// alt against a Latin-only pattern, which used to drop the whole attribute for
// Arabic punctuation and diacritics; the cms policy allows any printable alt.
func TestSanitizeKeepsLocalizedImageAlt(t *testing.T) {
	keep := []string{
		`شمس، وطاقة`,        // Arabic comma (U+060C)
		`«الطاقة المتجددة»`, // guillemets
		`مُحَرِّك`,          // combining diacritics (harakat)
		`طاقة شمسية`,        // plain Arabic
		`Solar, panels`,     // Latin with comma
		`واط·متر`,           // Arabic with a middle dot
		``,                  // decorative: empty alt stays
	}
	for _, alt := range keep {
		in := `<img src="/media/a.jpg" alt="` + alt + `">`
		out := Sanitize(in)
		if !strings.Contains(out, `src="/media/a.jpg"`) {
			t.Fatalf("image dropped for alt %q: %s", alt, out)
		}
		if alt == "" {
			continue
		}
		if !strings.Contains(out, `alt="`+alt+`"`) {
			t.Errorf("alt not preserved for %q: %s", alt, out)
		}
	}
	// Control characters in alt are still refused (the attribute is dropped,
	// the image kept).
	bad := Sanitize("<img src=\"/media/a.jpg\" alt=\"line\x01break\">")
	if strings.Contains(bad, "alt=") {
		t.Errorf("alt with a control character should be dropped: %s", bad)
	}
}
