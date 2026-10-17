package content

import (
	"context"
	"slices"
	"testing"
)

// Search is a substring match over the plain text of title, excerpt and body, so an Arabic word
// still matches with an article or a prefix attached (الكتاب، وبالكتاب), as the old LIKE search did.
func TestSearchMatchesArabicInsideWords(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "عن الكتاب"})
	mustCreate(t, s, Input{Type: "post", Title: "أخرى", Body: "<p>قرأت وبالكتابة نتعلم</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "لا علاقة"})
	page, err := s.Query(ctx, Query{Search: "كتاب"})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(page); len(got) != 2 || !slices.Contains(got, "عن الكتاب") || !slices.Contains(got, "أخرى") {
		t.Fatalf("arabic substring: %v", got)
	}
}

// The body is HTML: tags and attributes are not searchable, entities are decoded, and text
// in neighbouring elements does not run together.
func TestSearchIgnoresMarkup(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Markup", Body: `<p class="lead">fish &amp; chips</p><p>alpha</p><p>beta</p>`})
	for _, q := range []string{"class", "lead", "amp", "alphabeta"} {
		if page, _ := s.Query(ctx, Query{Search: q}); page.Total != 0 {
			t.Fatalf("%q matched the markup: %v", q, titles(page))
		}
	}
	for _, q := range []string{"fish & chips", "alpha"} {
		if page, _ := s.Query(ctx, Query{Search: q}); page.Total != 1 {
			t.Fatalf("%q: %v", q, titles(page))
		}
	}
}

// Queries shorter than three characters still work (a substring scan of the indexed text).
func TestSearchShortQueries(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Go 1.27", Body: "<p>x</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "Other", Body: "<p>y</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "Nothing", Body: `<p class="go">z</p>`})
	page, _ := s.Query(ctx, Query{Search: "go"})
	if got := titles(page); !slices.Equal(got, []string{"Go 1.27"}) {
		t.Fatalf("short query: %v", got)
	}
	if page, _ := s.Query(ctx, Query{Search: "ي"}); page.Total != 0 {
		t.Fatalf("one letter: %v", titles(page))
	}
}

// Without an explicit order, a title hit ranks above an excerpt hit, which ranks above a body hit.
func TestSearchRanksTitleFirst(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Body", Body: "<p>about gorun here</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "Excerpt", Excerpt: "gorun summary"})
	mustCreate(t, s, Input{Type: "post", Title: "GoRun guide"})
	page, err := s.Query(ctx, Query{Search: "gorun"})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(page); !slices.Equal(got, []string{"GoRun guide", "Excerpt", "Body"}) {
		t.Fatalf("rank: %v", got)
	}
	// an explicit order still wins
	page, _ = s.Query(ctx, Query{Search: "gorun", Order: []Order{{By: "title"}}})
	if got := titles(page); !slices.Equal(got, []string{"Body", "Excerpt", "GoRun guide"}) {
		t.Fatalf("explicit order: %v", got)
	}
}

// Editing an item re-indexes it, and deleting it for good drops it from the index.
func TestSearchFollowsWrites(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "post", Title: "Before title"})
	if _, err := s.Update(ctx, it.ID, Input{Title: "After title"}); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Query(ctx, Query{Search: "Before"}); page.Total != 0 {
		t.Fatalf("stale title: %v", titles(page))
	}
	if page, _ := s.Query(ctx, Query{Search: "After"}); page.Total != 1 {
		t.Fatalf("new title: %v", titles(page))
	}
	if err := s.Delete(ctx, it.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"content_fts", "content_fts_v2"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows after delete: %d %v", table, n, err)
		}
	}
}

// Content written before the index existed (or behind its back) is indexed at boot.
func TestSearchReindexesExistingContent(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Legacy row", Body: "<p>old text</p>"})
	if _, err := s.db.ExecContext(ctx, `DELETE FROM content_fts; DELETE FROM content_fts_v2`); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Query(ctx, Query{Search: "Legacy"}); page.Total != 0 {
		t.Fatal("fixture: the index should be empty")
	}
	if err := s.ensureSearch(ctx); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Query(ctx, Query{Search: "old text"}); page.Total != 1 {
		t.Fatalf("reindexed: %v", titles(page))
	}
}

// Arabic spelling variants match each other in both directions: alef with hamza and bare
// alef, taa marbuta and haa, text with and without harakat, alef maqsura and yaa.
func TestSearchNormalizesArabic(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "قصة أحمد"})
	mustCreate(t, s, Input{Type: "post", Title: "Other", Body: "<p>في المدرسة الكبيرة</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "Third", Excerpt: "عن مُحَمَّد"})
	mustCreate(t, s, Input{Type: "post", Title: "احمد ومدرسه ومحمد بلا همزة"})
	mustCreate(t, s, Input{Type: "post", Title: "يحيى"})
	mustCreate(t, s, Input{Type: "post", Title: "Unrelated"})
	cases := []struct {
		q    string
		want []string
	}{
		{"احمد", []string{"قصة أحمد", "احمد ومدرسه ومحمد بلا همزة"}},
		{"أحمد", []string{"قصة أحمد", "احمد ومدرسه ومحمد بلا همزة"}},
		{"إحمد", []string{"قصة أحمد", "احمد ومدرسه ومحمد بلا همزة"}},
		{"مدرسه", []string{"Other", "احمد ومدرسه ومحمد بلا همزة"}},
		{"مدرسة", []string{"Other", "احمد ومدرسه ومحمد بلا همزة"}},
		{"محمد", []string{"Third", "احمد ومدرسه ومحمد بلا همزة"}},
		{"مُحَمَّد", []string{"Third", "احمد ومدرسه ومحمد بلا همزة"}},
		{"محـــمد", []string{"Third", "احمد ومدرسه ومحمد بلا همزة"}},
		{"يحيي", []string{"يحيى"}},
	}
	for _, c := range cases {
		page, err := s.Query(ctx, Query{Search: c.q})
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		got := titles(page)
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%q: got %v, want %v", c.q, got, want)
		}
	}
	// results carry the stored text, not the folded one
	page, _ := s.Query(ctx, Query{Search: "احمد", Order: []Order{{By: "title"}}})
	if !slices.Contains(titles(page), "قصة أحمد") {
		t.Fatalf("display text: %v", titles(page))
	}
}

// A query whose letters are all removed by folding (only harakat or tatweel) matches nothing,
// and a query that folds below three letters takes the short path on the folded text.
func TestSearchNormalizedShortQueries(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "أب"})
	mustCreate(t, s, Input{Type: "post", Title: "Other"})
	for _, q := range []string{"ـــ", "َُِ"} {
		if page, err := s.Query(ctx, Query{Search: q}); err != nil || page.Total != 0 {
			t.Fatalf("%q: %v %v", q, titles(page), err)
		}
	}
	for _, q := range []string{"اب", "أَبٌ"} {
		if page, _ := s.Query(ctx, Query{Search: q}); !slices.Equal(titles(page), []string{"أب"}) {
			t.Fatalf("%q: %v", q, titles(page))
		}
	}
}

// English search is unaffected: still a case-insensitive substring match.
func TestSearchEnglishUnchanged(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Hello World", Body: "<p>Café culture</p>"})
	for _, q := range []string{"hello", "WORLD", "lo wo", "Café"} {
		if page, _ := s.Query(ctx, Query{Search: q}); page.Total != 1 {
			t.Fatalf("%q: %v", q, titles(page))
		}
	}
}

// The old content_fts keeps the plain (unfolded) text so an older binary still searches as it
// did; the new content_fts_v2 holds the folded text.
func TestSearchWritesBothIndexes(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "post", Title: "مُحَمَّد في المدرسة"})
	var v1, v2 string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM content_fts WHERE content_id = ?`, it.ID).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM content_fts_v2 WHERE content_id = ?`, it.ID).Scan(&v2); err != nil {
		t.Fatal(err)
	}
	if v1 != "مُحَمَّد في المدرسة" || v2 != "محمد في المدرسه" {
		t.Fatalf("v1 %q v2 %q", v1, v2)
	}
}

// First boot after the migration: content_fts is in step but content_fts_v2 is empty, so
// ensureSearch backfills it; Reindex rebuilds it too.
func TestSearchBackfillsV2(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "قصة أحمد"})
	clearV2 := func() {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM content_fts_v2`); err != nil {
			t.Fatal(err)
		}
		if page, _ := s.Query(ctx, Query{Search: "احمد"}); page.Total != 0 {
			t.Fatal("fixture: v2 should be empty")
		}
	}
	clearV2()
	if err := s.ensureSearch(ctx); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Query(ctx, Query{Search: "احمد"}); page.Total != 1 {
		t.Fatalf("backfill: %v", titles(page))
	}
	clearV2()
	if err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Query(ctx, Query{Search: "احمد"}); page.Total != 1 {
		t.Fatalf("reindex: %v", titles(page))
	}
	var v1 int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM content_fts WHERE title = 'قصة أحمد'`).Scan(&v1); err != nil || v1 != 1 {
		t.Fatalf("v1 kept original text: %d %v", v1, err)
	}
}
