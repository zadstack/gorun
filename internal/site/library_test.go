package site_test

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// libraryFixture: Arabic posts in two categories with a site-defined indexed
// "level" select, plus a draft that must never be listed or counted.
func libraryFixture(t *testing.T) *env {
	t.Helper()
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	post, err := cs.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	post.Fields = append(post.Fields, content.Field{Key: "level", Type: content.FieldSelect, Indexed: true,
		Label:   map[string]string{"en": "Level", "ar": "المستوى"},
		Options: []content.Option{{Value: "beginner", Label: map[string]string{"ar": "مبتدئ"}}, {Value: "advanced", Label: map[string]string{"ar": "متقدم"}}}})
	if err := cs.SaveType(ctx, *post); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"news", "guides"} {
		if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: s, Name: map[string]string{"news": "أخبار", "guides": "أدلة"}[s]}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(slug, cat, level, status string) {
		in := content.Input{Type: "post", Slug: slug, Title: slug, Locale: "ar", Status: content.Status(status),
			Terms: map[string][]string{"category": {cat}}, Fields: map[string]any{"level": level}}
		if _, err := cs.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	add("news-one", "news", "beginner", "published")
	add("guide-one", "guides", "beginner", "published")
	add("guide-two", "guides", "advanced", "published")
	add("guide-three", "guides", "advanced", "published")
	add("guide-draft", "guides", "advanced", "draft")
	return e
}

// listed returns the fixture posts linked in body, sorted (items published
// in the same second keep the listing's own tie order).
func listed(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`/post/(news-one|guide-one|guide-two|guide-three|guide-draft)"`).FindAllStringSubmatch(body, -1) {
		if len(out) == 0 || out[len(out)-1] != m[1] {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// Type archives filter by the type's taxonomies and indexed select fields,
// over published items only, with facet counts that respect the other active
// filter; unknown keys and invalid values are ignored and never echoed.
func TestLibraryFiltersOnTheBlogArchive(t *testing.T) {
	e := libraryFixture(t)
	resp, body := e.get("/blog")
	if resp.StatusCode != 200 {
		t.Fatalf("/blog: %d", resp.StatusCode)
	}
	mustContain(t, body, `class="gp-library-filters"`, `href="/blog?category=guides"`, `href="/blog?level=advanced"`, "4 نتيجة")
	if strings.Contains(body, "guide-draft") {
		t.Fatal("a draft reached the archive")
	}

	_, body = e.get("/blog?category=guides")
	if got := strings.Join(listed(body), ","); got != "guide-one,guide-three,guide-two" {
		t.Fatalf("category=guides listed %s", got)
	}
	// Facet counts under category=guides: one beginner, two advanced (the draft does not count).
	if !regexp.MustCompile(`level=beginner"[^>]*>مبتدئ <span class="gp-filter-count">1</span>`).MatchString(body) ||
		!regexp.MustCompile(`level=advanced"[^>]*>متقدم <span class="gp-filter-count">2</span>`).MatchString(body) {
		t.Fatalf("facet counts under the category filter are wrong:\n%s", body)
	}
	mustContain(t, body, "3 نتيجة", `class="gp-filter-chip" href="/blog"`, `class="gp-filter-clear" href="/blog"`)

	_, body = e.get("/blog?category=guides&level=advanced")
	if got := strings.Join(listed(body), ","); got != "guide-three,guide-two" {
		t.Fatalf("guides+advanced listed %s", got)
	}

	_, body = e.get("/blog?category=news&level=advanced")
	mustContain(t, body, `class="gp-library-empty"`, "لا توجد عناصر منشورة تطابق هذه التصفية.")

	_, body = e.get("/blog?level=expert&evil=%3Cscript%3E&category=..%2F..")
	if len(listed(body)) != 4 || strings.Contains(body, "expert") || strings.Contains(body, "<script>") || strings.Contains(body, "evil") {
		t.Fatalf("invalid filters must be ignored and never echoed:\n%s", body)
	}
}

// Pagination keeps the active filters, and a term archive is a library of
// its type with the term fixed (not offered as its own filter).
func TestLibraryPaginationAndTermArchive(t *testing.T) {
	e := libraryFixture(t)
	e.setting("site.posts_per_page", 1)
	_, body := e.get("/blog?category=guides")
	mustContain(t, body, `href="/blog?category=guides&amp;page=2"`)
	_, body = e.get("/blog?category=guides&page=2&evil=1")
	mustContain(t, body, `href="/blog?category=guides"`)
	if strings.Contains(body, "evil") {
		t.Fatal("pagination echoed an unknown parameter")
	}

	e.setting("site.posts_per_page", 10)
	_, body = e.get("/category/guides?level=beginner")
	if got := strings.Join(listed(body), ","); got != "guide-one" {
		t.Fatalf("term archive with level=beginner listed %s", got)
	}
	if strings.Contains(body, "?category=") {
		t.Fatal("the term archive must not offer its own taxonomy as a filter")
	}
}

// A series archive lists the whole series in series order (numbered parts by
// number, then the unnumbered), whatever order the parts were published in,
// and never a draft part.
func TestSeriesArchiveListsPartsInSeriesOrder(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "series", Slug: "start", Name: "ابدأ"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		slug, status string
		order        any
	}{{"part-b", "published", 2}, {"part-extra", "published", nil}, {"part-a", "published", 1}, {"part-draft", "draft", 3}, {"part-c", "published", 3}} {
		fields := map[string]any{}
		if p.order != nil {
			fields["series_order"] = p.order
		}
		if _, err := cs.Create(ctx, content.Input{Type: "post", Slug: p.slug, Title: p.slug, Locale: "ar", Status: content.Status(p.status),
			Terms: map[string][]string{"series": {"start"}}, Fields: fields}); err != nil {
			t.Fatal(err)
		}
	}
	e.setting("site.posts_per_page", 2)
	resp, body := e.get("/series/start")
	if resp.StatusCode != 200 {
		t.Fatalf("/series/start: %d", resp.StatusCode)
	}
	var got []string
	for _, m := range regexp.MustCompile(`/post/(part-[a-z]+)"`).FindAllStringSubmatch(body, -1) {
		if len(got) == 0 || got[len(got)-1] != m[1] {
			got = append(got, m[1])
		}
	}
	if strings.Join(got, ",") != "part-a,part-b,part-c,part-extra" {
		t.Fatalf("series archive order = %v", got)
	}
}
