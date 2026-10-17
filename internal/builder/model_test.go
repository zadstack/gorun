package builder

import (
	"github.com/zadstack/gorun/internal/content"
	"strings"
	"testing"
)

func TestRenderEscapesAndSupportsCatalog(t *testing.T) {
	if len(Catalog("ar")) != 12 {
		t.Fatal("expected twelve sections")
	}
	for _, spec := range Catalog("en") {
		s := DefaultSection(spec.Kind, "en")
		s.Title = `<script>alert("x")</script>`
		got, err := Render(Document{Version: 1, Sections: []Section{s}})
		if err != nil {
			t.Fatalf("%s: %v", spec.Kind, err)
		}
		if strings.Contains(got, "<script>") || !strings.Contains(got, "&lt;script&gt;") {
			t.Fatalf("unescaped %s", spec.Kind)
		}
	}
}
func TestValidateRejectsUnsafeInput(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "//evil.test/x", "data:image/svg+xml,x", "https://user:pass@example.test", "https://example.test/\nfoo", "/\\evil.test"} {
		s := DefaultSection("hero", "en")
		s.Link = bad
		if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	s := DefaultSection("hero", "en")
	s.Variant = "<script>"
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("unsafe variant")
	}
}
func TestRenderHiddenAndSafeLinks(t *testing.T) {
	s := DefaultSection("hero", "ar")
	s.Hidden = true
	html, err := Render(Document{Version: 1, Sections: []Section{s}})
	if err != nil || strings.Contains(html, s.Title) {
		t.Fatalf("hidden %s %v", html, err)
	}
	for _, u := range []string{"/media/image.png", "https://example.test/x", "#contact", "mailto:a@example.test", "tel:+20123456789"} {
		s.Hidden = false
		s.Link = u
		if err := Validate(Document{Version: 1, Sections: []Section{s}}); err != nil {
			t.Fatal(err)
		}
	}
	s.Image = "mailto:a@example.test"
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("image accepted mailto")
	}
}

func TestFeedIsRestrictedAndHasMarker(t *testing.T) {
	s := DefaultSection("projects", "en")
	s.Feed = "posts"
	got, err := Render(Document{Version: 1, Sections: []Section{s}})
	if err != nil || !strings.Contains(got, "gp-feed-posts") {
		t.Fatalf("feed %s %v", got, err)
	}
	s.Feed = "arbitrary"
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("unsupported feed")
	}
	s.Feed = "posts"
	s.Kind = "hero"
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("unsupported feed kind")
	}
}

func TestFirstHeroHasOnlyMainHeadingAndFAQUsesDisclosure(t *testing.T) {
	a := DefaultSection("hero", "en")
	b := DefaultSection("hero", "en")
	q := DefaultSection("faq", "en")
	got, err := Render(Document{Version: 1, Sections: []Section{a, b, q}})
	if err != nil || strings.Count(got, "<h1>") != 1 || !strings.Contains(got, "<details") || !strings.Contains(got, "<summary>") {
		t.Fatalf("headings/disclosures %s %v", got, err)
	}
}

func TestFirstVisibleTitledSectionGetsMainHeading(t *testing.T) {
	hidden := DefaultSection("hero", "en")
	hidden.Hidden = true
	blank := DefaultSection("text", "en")
	blank.Title = " \n "
	text := DefaultSection("text", "en")
	text.Title = "Visible text"
	hero := DefaultSection("hero", "en")
	got, err := Render(Document{Version: 1, Sections: []Section{hidden, blank, text, hero}})
	if err != nil || !strings.Contains(got, "<h1>Visible text</h1>") || strings.Count(got, "<h1>") != 1 {
		t.Fatalf("headings %s %v", got, err)
	}
}

func TestOmittedLayoutKeepsPublicSectionClassesAndSafeAnchors(t *testing.T) {
	doc := Document{Version: 1, Sections: []Section{{ID: "section-one", Kind: "text", Title: "First section"}}}
	markup, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := content.Sanitize(markup)
	if !strings.Contains(cleaned, `class="gp-section gp-text gp-default"`) || !strings.Contains(cleaned, `id="gp-section-one"`) {
		t.Fatalf("public markup lost layout or anchor: %s", cleaned)
	}
}
