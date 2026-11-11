package content

import (
	"reflect"
	"strings"
	"testing"
)

func TestHTMLTextLinesKeepsBlocks(t *testing.T) {
	got := HTMLTextLines(`<h2>Title &amp; more</h2><p>One  <strong>two</strong>
	three</p><ul><li>a</li><li>b<br>c</li></ul><script>alert(1)</script><p><img src="/x.png" alt="Logo"></p><pre>  keep
  spaces</pre>`)
	want := []string{"Title & more", "One two three", "a", "b", "c", "[image: Logo]", "  keep\n  spaces"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines\n got %q\nwant %q", got, want)
	}
}

func TestDiffWordsMarksChangesAndJoinsBack(t *testing.T) {
	a, b := "the quick brown fox", "the slow brown fox jumps"
	spans := DiffWords(a, b)
	var oldText, newText strings.Builder
	for _, s := range spans {
		if s.Op != DiffInsert {
			oldText.WriteString(s.Text)
		}
		if s.Op != DiffDelete {
			newText.WriteString(s.Text)
		}
	}
	if oldText.String() != a || newText.String() != b {
		t.Fatalf("spans do not rebuild the texts: %q / %q", oldText.String(), newText.String())
	}
	want := []DiffSpan{{DiffEqual, "the "}, {DiffDelete, "quick"}, {DiffInsert, "slow"}, {DiffEqual, " brown fox"}, {DiffInsert, " jumps"}}
	if !reflect.DeepEqual(spans, want) {
		t.Fatalf("spans %+v", spans)
	}
	// Arabic words diff the same way.
	ar := DiffWords("مرحبا بالعالم", "مرحبا بالجميع")
	if len(ar) != 3 || ar[1].Op != DiffDelete || ar[1].Text != "بالعالم" || ar[2].Text != "بالجميع" {
		t.Fatalf("arabic %+v", ar)
	}
}

func TestDiffLinesPairsChangedLines(t *testing.T) {
	got := DiffLines([]string{"keep", "old line", "drop"}, []string{"keep", "new line", "added"})
	ops := []DiffOp{}
	for _, l := range got {
		ops = append(ops, l.Op)
	}
	if !reflect.DeepEqual(ops, []DiffOp{DiffEqual, DiffChange, DiffChange}) {
		t.Fatalf("ops %v", ops)
	}
	got = DiffLines([]string{"a"}, []string{"a", "b", "c"})
	if len(got) != 3 || got[1].Op != DiffInsert || got[2].Op != DiffInsert {
		t.Fatalf("inserts %+v", got)
	}
	got = DiffLines([]string{"a", "b"}, []string{"b"})
	if len(got) != 2 || got[0].Op != DiffDelete || got[1].Op != DiffEqual {
		t.Fatalf("delete %+v", got)
	}
}

func TestDiffFallsBackOnHugeInput(t *testing.T) {
	a, b := make([]string, 2000), make([]string, 2000)
	for i := range a {
		a[i], b[i] = "a"+strings.Repeat("x", i), "b"+strings.Repeat("y", i)
	}
	got := diffSeq(a, b)
	if len(got) != 4000 || got[0].Op != DiffDelete || got[3999].Op != DiffInsert {
		t.Fatalf("fallback %d", len(got))
	}
}

func TestCompareItemsListsChangedFields(t *testing.T) {
	old := &Item{Title: "Old", Slug: "s", Status: "draft", SEO: map[string]any{"title": "A"}, Fields: map[string]any{"price": 1.0},
		Terms: map[string][]string{"tag": {"x"}}, Body: "<p>Same</p><p>Old para</p>"}
	cur := &Item{Title: "New", Slug: "s", Status: "published", SEO: map[string]any{"title": "B", "robots": "noindex"}, Fields: map[string]any{"price": 2.0},
		Terms: map[string][]string{"tag": {"x", "y"}}, Body: "<p>Same</p><p>New para</p>"}
	fields, body := CompareItems(old, cur)
	keys := []string{}
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	want := []string{"title", "status", "seo.robots", "seo.title", "fields.price", "terms.tag"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys %v", keys)
	}
	if len(body) != 2 || body[0].Op != DiffEqual || body[1].Op != DiffChange {
		t.Fatalf("body %+v", body)
	}
}
