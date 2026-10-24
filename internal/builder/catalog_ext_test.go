package builder

import (
	"strings"
	"testing"
)

// Text-and-media rows, feature switchers and step images render through the
// native library; entry images are allowed only where the kind uses them,
// and a switcher option needs a label.
func TestNativeCatalogExtensions(t *testing.T) {
	rows := Section{Library: SectionLibrary, ID: "rows", Kind: "media_text", Title: "How it fits", Items: []Entry{
		{Title: "Plan", Text: "Plan the page.", Image: "/media/plan.jpg", ImageAlt: "A planning board"},
		{Title: "Write", Text: "Write the copy."},
	}}
	sw := Section{Library: SectionLibrary, ID: "sw", Kind: "switcher", Title: "Features", Items: []Entry{
		{Title: "Drafts", Text: "Private until published.", Image: "/media/d.png", ImageAlt: "Draft list"},
		{Title: "Review", Text: "Reviewed before publishing."},
	}}
	steps := Section{Library: SectionLibrary, ID: "steps", Kind: "steps", Title: "Steps", Items: []Entry{{Title: "One", Image: "/media/1.jpg", ImageAlt: "Step one"}}}
	doc := Document{Version: 1, Sections: []Section{{Library: SectionLibrary, ID: "hero", Kind: "hero", Title: "Atlas"}, rows, sw, steps}}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	html, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<div class="gp-s-rows"><div class="gp-s-row"><figure class="gp-s-row-media"><img src="/media/plan.jpg" alt="A planning board"`,
		`<div class="gp-s-row gp-s-row-text">`,
		`<details class="gp-s-switch" name="gp-switch-sw" open><summary>Drafts</summary>`,
		`<details class="gp-s-switch" name="gp-switch-sw"><summary>Review</summary>`,
		`<figure class="gp-s-step-media"><img src="/media/1.jpg" alt="Step one"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("render lacks %s\n%s", want, html)
		}
	}

	bad := map[string]Section{
		"faq entry image":        {Library: SectionLibrary, ID: "f", Kind: "faq", Items: []Entry{{Title: "Q", Image: "/media/x.jpg"}}},
		"switcher without label": {Library: SectionLibrary, ID: "s", Kind: "switcher", Items: []Entry{{Title: " ", Text: "x"}}},
		"row link":               {Library: SectionLibrary, ID: "r", Kind: "media_text", Items: []Entry{{Title: "x", Link: "/x"}}},
		"legacy media_text":      {ID: "l", Kind: "media_text"},
	}
	for name, s := range bad {
		if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
