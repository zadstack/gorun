package builder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestTaskEntrySearchSurvivesPublicationSanitizer(t *testing.T) {
	s := DefaultSection("task_entry", "ar")
	s.Variant = "search_first"
	s.Native.Search = &NativeSearchOptions{Action: "/ar/search", Label: "ابحث عن خدمة", Placeholder: "الصحة والتعليم", Param: "q"}
	s.Title = "اختر خطوتك"
	doc := Document{Version: 1, Sections: []Section{s}}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	var loaded Document
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	html, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	clean := content.Sanitize(html)
	for _, want := range []string{`gp-task-search gp-task-param-q`, `href="/ar/search"`, `ابحث عن خدمة`, `الصحة والتعليم`} {
		if !strings.Contains(clean, want) {
			t.Fatalf("publication lost %q: %s", want, clean)
		}
	}
	if strings.Contains(clean, "<script") {
		t.Fatal("unexpected script")
	}
}

func TestTaskEntryRejectsInvalidSearchAndCrossKindOptions(t *testing.T) {
	for _, action := range []string{"https://outside.example/search", "javascript:alert(1)", "//outside.example/search", "mailto:a@example.test", "#search", "/search?scope=private", "/search#results", "/%2foutside.example", "/%5coutside.example", "/search%0A", "/search?", "/search#"} {
		s := DefaultSection("task_entry", "en")
		s.Variant = "search_first"
		s.Native.Search = &NativeSearchOptions{Action: action, Label: "Search"}
		if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
			t.Fatalf("accepted %q", action)
		}
	}
	s := DefaultSection("hero", "en")
	s.Library = SectionLibrary
	s.Native = &NativeOptions{Search: &NativeSearchOptions{Action: "/search", Label: "Search"}}
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("search option leaked into hero")
	}
	s = DefaultSection("task_entry", "en")
	s.Variant = "search_first"
	s.Native.Search = &NativeSearchOptions{Action: "/search", Label: "Search", Param: `q" autofocus`}
	if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
		t.Fatal("unsafe parameter accepted")
	}
}

func TestFeatureLayoutsKeepLinksAndEscapeContent(t *testing.T) {
	for _, variant := range []string{"compact_rows", "image_led", "featured_secondary"} {
		s := DefaultSection("features", "ar")
		s.Library = SectionLibrary
		s.Native = &NativeOptions{EmptyText: "لا توجد عناصر"}
		s.Variant = variant
		s.Items = []Entry{{Title: `<img src=x onerror=alert(1)>`, Text: "توضيح", Link: "/ar/service", Image: "/media/example.webp", ImageAlt: "مثال"}, {Title: "الثاني", Link: "/ar/second", Featured: true}}
		if err := Validate(Document{Version: 1, Sections: []Section{s}}); err != nil {
			t.Fatal(err)
		}
		html, err := Render(Document{Version: 1, Sections: []Section{s}})
		if err != nil {
			t.Fatal(err)
		}
		clean := content.Sanitize(html)
		if !strings.Contains(clean, `href="/ar/service"`) || !strings.Contains(clean, "&lt;img") || strings.Contains(clean, `<img src=x`) {
			t.Fatalf("unsafe or lost layout: %s", clean)
		}
		if variant == "featured_secondary" && strings.Count(clean, "gp-feature-primary") != 1 {
			t.Fatal("layout needs exactly one primary entry")
		}
		for i := range s.Items {
			s.Items[i].Image = ""
			s.Items[i].ImageAlt = ""
		}
		withoutImages, err := Render(Document{Version: 1, Sections: []Section{s}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(withoutImages, "<figure") || !strings.Contains(withoutImages, `href="/ar/service"`) {
			t.Fatal("missing images must retain useful text links")
		}
		s.Items = nil
		html, err = Render(Document{Version: 1, Sections: []Section{s}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html, "لا توجد عناصر") {
			t.Fatal("configured empty state missing")
		}
	}
}

func TestTaskChoicesRequireRealDestinations(t *testing.T) {
	for _, entries := range [][]Entry{nil, {{Title: "Choose"}}, {{Link: "/choice"}}, {{Title: "Choose", Link: "/choice", Featured: true}}} {
		s := DefaultSection("task_entry", "en")
		s.Items = entries
		if Validate(Document{Version: 1, Sections: []Section{s}}) == nil {
			t.Fatalf("invalid choices accepted: %#v", entries)
		}
	}
	s := DefaultSection("task_entry", "ar")
	s.Variant = "service_first"
	s.Items = []Entry{{Title: "مراجعة الخدمة", Text: "النطاق والمخرجات قبل الطلب", Link: "/ar/services"}}
	html, err := Render(Document{Version: 1, Sections: []Section{s}})
	if err != nil {
		t.Fatal(err)
	}
	clean := content.Sanitize(html)
	if !strings.Contains(clean, `href="/ar/services"`) || strings.Contains(clean, "<form") {
		t.Fatalf("service choice must remain a navigation link: %s", clean)
	}
}
