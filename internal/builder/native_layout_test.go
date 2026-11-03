package builder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// New layouts survive the same JSON reload and sanitizer as published pages;
// unsafe or cross-kind layout values never reach the rendered class attribute.
func TestNativeLayoutsRoundTripAndRender(t *testing.T) {
	for _, test := range []struct{ kind, variant, class string }{
		{"directory", "compact", "gp-s-directory gp-s-dir-post gp-s-directory-compact"},
		{"media_text", "image_start", "gp-s-rows gp-s-rows-image-start"},
		{"media_text", "image_end", "gp-s-rows gp-s-rows-image-end"},
	} {
		t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
			section := DefaultSection(test.kind, "ar")
			section.Variant = test.variant
			section.Title = `<script>unsafe</script>`
			doc := Document{Version: 1, Sections: []Section{section}}
			if err := Validate(doc); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var loaded Document
			if err = json.Unmarshal(data, &loaded); err != nil {
				t.Fatal(err)
			}
			sameDocument(t, doc, loaded)
			rendered, err := Render(loaded)
			if err != nil {
				t.Fatal(err)
			}
			cleaned := content.Sanitize(rendered)
			if !strings.Contains(cleaned, `class="`+test.class+`"`) || strings.Contains(cleaned, "<script>") || !strings.Contains(cleaned, "&lt;script&gt;") {
				t.Fatalf("layout lost or unsafe markup: %s", cleaned)
			}
		})
	}
}

func TestNativeLayoutRejectsUnsupportedKindsAndLegacy(t *testing.T) {
	for _, test := range []struct{ kind, variant, library string }{
		{"directory", "image_start", SectionLibrary},
		{"media_text", "compact", SectionLibrary},
		{"features", "compact", SectionLibrary},
		{"faq", "centered", SectionLibrary},
		{"hero", `split" onclick="alert(1)`, SectionLibrary},
		{"features", "compact", ""},
		{"text", "image_end", ""},
	} {
		t.Run(test.kind+"/"+test.variant+"/"+test.library, func(t *testing.T) {
			section := DefaultSection(test.kind, "en")
			section.Library = test.library
			if test.library == SectionLibrary && section.Native == nil {
				section.Native = &NativeOptions{}
			}
			section.Variant = test.variant
			if Validate(Document{Version: 1, Sections: []Section{section}}) == nil {
				t.Fatal("unsupported layout accepted")
			}
		})
	}
}

func TestNativeDefaultLayoutsRemainUnmarked(t *testing.T) {
	for _, kind := range []string{"directory", "media_text", "features"} {
		section := DefaultSection(kind, "en")
		section.Library = SectionLibrary
		// Empty layout was accepted before default became an explicit editor choice.
		explicit, err := Render(Document{Version: 1, Sections: []Section{section}})
		if err != nil {
			t.Fatal(err)
		}
		section.Variant = ""
		implicit, err := Render(Document{Version: 1, Sections: []Section{section}})
		if err != nil {
			t.Fatal(err)
		}
		if explicit != implicit {
			t.Fatalf("default and unset layout differ for %s", kind)
		}
		if strings.Contains(explicit, "gp-s-directory-compact") || strings.Contains(explicit, "gp-s-rows-image-") {
			t.Fatalf("existing %s acquired an opt-in layout", kind)
		}
		if kind == "media_text" && !strings.Contains(explicit, `<div class="gp-s-rows">`) {
			t.Fatal("default alternating-row hook changed")
		}
	}
}
