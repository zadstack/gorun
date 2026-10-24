package builder

import (
	"strings"
	"testing"
)

// A directory names the content type it lists; the page renders a slot the
// theme fills with a search form and results at request time.
func TestDirectorySectionRendersTypedSlot(t *testing.T) {
	doc := Document{Version: 1, Sections: []Section{{Library: SectionLibrary, ID: "dir", Kind: "directory", Title: "Guides", Native: &NativeOptions{ListType: "guide"}}}}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	html, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<div class="gp-s-directory gp-s-dir-guide"></div>`) {
		t.Fatalf("directory slot missing:\n%s", html)
	}
	if d := DefaultSection("directory", "en"); d.Native == nil || d.Native.ListType != "post" || Validate(Document{Version: 1, Sections: []Section{d}}) != nil {
		t.Fatalf("default directory is not valid: %+v", d)
	}
	bad := map[string]Section{
		"no type":        {Library: SectionLibrary, ID: "a", Kind: "directory", Native: &NativeOptions{}},
		"no options":     {Library: SectionLibrary, ID: "b", Kind: "directory"},
		"unsafe type":    {Library: SectionLibrary, ID: "c", Kind: "directory", Native: &NativeOptions{ListType: `post" onclick="x`}},
		"type elsewhere": {Library: SectionLibrary, ID: "d", Kind: "features", Native: &NativeOptions{ListType: "post"}},
		"items":          {Library: SectionLibrary, ID: "e", Kind: "directory", Native: &NativeOptions{ListType: "post"}, Items: []Entry{{Title: "x"}}},
		"legacy":         {ID: "f", Kind: "directory"},
	}
	for name, s := range bad {
		if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
