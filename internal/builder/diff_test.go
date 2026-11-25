package builder

import (
	"strings"
	"testing"
)

func TestDiffReadsAsAPlan(t *testing.T) {
	hero := DefaultSection("hero", "en")
	hero.ID, hero.Title = "h", "Our services"
	feats := DefaultSection("features", "en")
	feats.ID = "f"
	cta := DefaultSection("cta", "en")
	cta.ID = "c"
	before := Document{Version: 1, Sections: []Section{hero, feats, cta}}

	hero2 := hero
	hero2.Title = "Services we offer"
	feats2 := feats
	feats2.Items = append([]Entry{}, feats.Items...)
	feats2.Items[1].Text = "Go, SQLite and themes."
	feats2.Items = feats2.Items[:2]
	feats2.Hidden = true
	faq := DefaultSection("faq", "en")
	faq.ID = "q"
	after := Document{Version: 1, Sections: []Section{feats2, hero2, faq}}

	d := Diff(before, after)
	want := []string{
		"- cta c: Call to action",
		"↕ features f: 2 → 1",
		"~ features f items[2].text: Add a short description → Go, SQLite and themes.",
		"~ features f hidden: false → true",
		"- features f items[3]: Item 3",
		"↕ hero h: 1 → 2",
		"~ hero h title: Our services → Services we offer",
		"+ faq q: Questions",
	}
	text := d.Text()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("diff lacks %q:\n%s", w, text)
		}
	}
	if d.Summary != "1 added, 2 removed, 2 moved, 3 changed" {
		t.Errorf("summary = %q", d.Summary)
	}
	if !Diff(before, before).Empty() || Diff(before, before).Text() != "no changes" {
		t.Error("identical documents must diff empty")
	}

	// Adding a section at the top is an addition, not a move of the rest.
	withNew := Document{Version: 1, Sections: append([]Section{faq}, before.Sections...)}
	for _, c := range Diff(before, withNew).Changes {
		if c.Op == "move" {
			t.Errorf("addition at the top read as a move: %+v", c)
		}
	}

	// Native options and long values.
	n1 := DefaultSection("steps", "en")
	n1.ID = "s"
	n2 := n1
	n2.Native = &NativeOptions{Background: "dots"}
	n2.Text = strings.Repeat("long ", 40)
	d = Diff(Document{Version: 1, Sections: []Section{n1}}, Document{Version: 1, Sections: []Section{n2}})
	text = d.Text()
	if !strings.Contains(text, "native.background:  → dots") || !strings.Contains(text, "…") {
		t.Errorf("native diff / shortening: %s", text)
	}
}
