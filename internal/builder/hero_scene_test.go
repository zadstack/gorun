package builder

import (
	"strings"
	"testing"
)

func TestHeroSceneValidationAndRendering(t *testing.T) {
	for _, scene := range []string{"", "orbit", "ribbons", "blocks"} {
		doc := Document{Version: 1, Sections: []Section{{ID: "hero", Kind: "hero", Title: "Safe title", HeroScene: scene}}}
		out, err := Render(doc)
		if err != nil {
			t.Fatal(err)
		}
		if (scene != "") != strings.Contains(out, "gp-hero-scene") {
			t.Fatalf("scene %q markup: %s", scene, out)
		}
		if strings.Contains(out, "<script") {
			t.Fatal("stored content must not contain executable script")
		}
	}
	for _, section := range []Section{
		{ID: "x", Kind: "hero", HeroScene: "https://evil.test/x.js"},
		{ID: "x", Kind: "text", HeroScene: "orbit"},
	} {
		if Validate(Document{Version: 1, Sections: []Section{section}}) == nil {
			t.Fatal("invalid scene accepted")
		}
	}
}
