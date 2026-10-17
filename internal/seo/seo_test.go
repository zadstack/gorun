package seo

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func testService(t *testing.T) *service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	return &service{settings: settings.New(d), hooks: hooks.New(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		baseURL:  "http://config.example",
		content:  func() (content.Service, bool) { return nil, false },
		taxonomy: func() (taxonomy.Service, bool) { return nil, false }}
}

func set(t *testing.T, s *service, kv ...string) {
	t.Helper()
	for i := 0; i+1 < len(kv); i += 2 {
		if err := s.settings.Set(context.Background(), kv[i], kv[i+1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSEORenderEscapesAndFallsBack(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	set(t, s, SettingSiteTitle, "Zad & Co", SettingSiteTagline, "Self-hosted <AI>", SettingSiteBaseURL, "https://zad.example/",
		SettingDefaultOGImage, "/media/2026/10/og.png")

	// Home page: nothing set → site title + tagline, tagline as description.
	out := string(s.Render(ctx, Meta{}, Meta{}))
	for _, want := range []string{
		"<title>Zad &amp; Co – Self-hosted &lt;AI&gt;</title>",
		`<meta name="description" content="Self-hosted &lt;AI&gt;">`,
		`<meta property="og:image" content="https://zad.example/media/2026/10/og.png">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta property="og:site_name" content="Zad &amp; Co">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("home: missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "canonical") || strings.Contains(out, "robots") || strings.Contains(out, "ld+json") {
		t.Errorf("home: unexpected tags\n%s", out)
	}

	// Content page: fallback title goes through the template; item values win.
	evil := `"><script>alert(1)</script>`
	schema := json.RawMessage(`{"@type":"Article","headline":"</script><script>alert(2)</script>"}`)
	out = string(s.Render(ctx,
		Meta{Description: evil, Robots: "noindex, follow", OGImage: "javascript:alert(3)", Schema: schema},
		Meta{Title: "Hello <World>", Canonical: "/hello", Description: "ignored"}))
	for _, want := range []string{
		"<title>Hello &lt;World&gt; – Zad &amp; Co</title>",
		`<meta name="description" content="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">`,
		`<meta name="robots" content="noindex, follow">`,
		`<link rel="canonical" href="https://zad.example/hello">`,
		`<meta property="og:url" content="https://zad.example/hello">`,
		`<meta property="og:title" content="Hello &lt;World&gt;">`,
		`<meta property="og:image" content="https://zad.example/media/2026/10/og.png">`, // invalid item image → default
		`\u003c/script\u003e\u003cscript\u003ealert(2)\u003c/script\u003e`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page: missing %s in\n%s", want, out)
		}
	}
	if strings.Count(out, "<script") != 1 || strings.Contains(out, "javascript:") {
		t.Errorf("page: injection survived\n%s", out)
	}

	// Explicit SEO title is used verbatim; custom template applies otherwise.
	set(t, s, SettingTitleTemplate, "{site} | {title}")
	if out := string(s.Render(ctx, Meta{Title: "Exact Title"}, Meta{Title: "T"})); !strings.Contains(out, "<title>Exact Title</title>") {
		t.Errorf("explicit title:\n%s", out)
	}
	if out := string(s.Render(ctx, Meta{}, Meta{Title: "T"})); !strings.Contains(out, "<title>Zad &amp; Co | T</title>") {
		t.Errorf("custom template:\n%s", out)
	}
	// Invalid or scalar schema is dropped; no site settings → config base URL.
	if out := string(s.Render(ctx, Meta{Schema: json.RawMessage(`{bad`)}, Meta{})); strings.Contains(out, "ld+json") {
		t.Errorf("invalid schema rendered:\n%s", out)
	}
	s2 := testService(t)
	if out := string(s2.Render(ctx, Meta{}, Meta{Title: "Only", Canonical: "/x"})); !strings.Contains(out, "<title>Only</title>") ||
		!strings.Contains(out, `href="http://config.example/x"`) || !strings.Contains(out, `content="summary"`) {
		t.Errorf("bare site:\n%s", out)
	}
}

// fakeContent honors Type, Status and paging in Query.
type fakeContent struct {
	content.Service
	types []content.Type
	items []content.Item
}

func (f fakeContent) Types(context.Context) ([]content.Type, error) { return f.types, nil }

func (f fakeContent) Query(_ context.Context, q content.Query) (content.Page[content.Item], error) {
	var match []content.Item
	for _, it := range f.items {
		if (q.Type == "" || it.Type == q.Type) && (q.Status == "" || string(it.Status) == q.Status) {
			match = append(match, it)
		}
	}
	start := min((q.Page-1)*q.PerPage, len(match))
	end := min(start+q.PerPage, len(match))
	return content.Page[content.Item]{Items: match[start:end], Total: len(match), Page: q.Page, PerPage: q.PerPage}, nil
}

type fakeTax struct {
	taxonomy.Service
	taxes []taxonomy.Taxonomy
	terms map[string][]taxonomy.Term
}

func (f fakeTax) Taxonomies(context.Context) ([]taxonomy.Taxonomy, error) { return f.taxes, nil }
func (f fakeTax) Terms(_ context.Context, tax string, _ taxonomy.TermQuery) ([]taxonomy.Term, int, error) {
	return f.terms[tax], len(f.terms[tax]), nil
}

func TestSitemapIncludesPublishedOnly(t *testing.T) {
	s := testService(t)
	set(t, s, SettingSiteBaseURL, "https://site.example")
	fc := fakeContent{
		types: []content.Type{
			{Slug: "post", Public: true, HasArchive: true, ArchiveSlug: "blog"},
			{Slug: "page", Public: true},
			{Slug: "secret", Public: false},
		},
		items: []content.Item{
			{ID: "1", Type: "post", Slug: "hello", Status: "published", UpdatedAt: "2026-10-01T00:00:00Z"},
			{ID: "2", Type: "post", Slug: "draft-post", Status: "draft"},
			{ID: "3", Type: "page", Slug: "about", Status: "published"},
			{ID: "4", Type: "page", Slug: "private-page", Status: "private"},
			{ID: "5", Type: "secret", Slug: "hidden", Status: "published"},
			{ID: "6", Type: "page", Slug: "noindexed", Status: "published", SEO: map[string]any{"robots": "noindex"}},
			{ID: "7", Type: "post", Slug: "a&b", Status: "published"},
		},
	}
	for i := range 150 { // forces a second Query page
		fc.items = append(fc.items, content.Item{ID: "p" + string(rune('a'+i%26)), Type: "page", Slug: "bulk-" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26)), Status: "published"})
	}
	ft := fakeTax{taxes: []taxonomy.Taxonomy{{Slug: "category", Public: true}, {Slug: "internal", Public: false}},
		terms: map[string][]taxonomy.Term{"category": {{Taxonomy: "category", Slug: "news"}}, "internal": {{Taxonomy: "internal", Slug: "x"}}}}
	s.content = func() (content.Service, bool) { return fc, true }
	s.taxonomy = func() (taxonomy.Service, bool) { return ft, true }

	out, err := s.Sitemap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var set xmlURLSet
	if err := xml.Unmarshal(out, &set); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, out)
	}
	locs := map[string]string{}
	for _, u := range set.URLs {
		locs[u.Loc] = u.LastMod
	}
	for _, want := range []string{"https://site.example/", "https://site.example/hello", "https://site.example/about",
		"https://site.example/blog/", "https://site.example/category/news", "https://site.example/a&b"} {
		if _, ok := locs[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"draft-post", "private-page", "hidden", "noindexed", "/internal/"} {
		for loc := range locs {
			if strings.Contains(loc, bad) {
				t.Errorf("must not list %s", loc)
			}
		}
	}
	if locs["https://site.example/hello"] != "2026-10-01T00:00:00Z" {
		t.Errorf("lastmod: %q", locs["https://site.example/hello"])
	}
	if !strings.Contains(string(out), "a&amp;b") || !strings.Contains(string(out), `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`) {
		t.Errorf("escaping/namespace:\n%.400s", out)
	}
	if n := len(set.URLs); n < 150 {
		t.Errorf("paging lost items: %d", n)
	}
	// The filter can rewrite permalinks.
	s.hooks.Filter(FilterSitemapEntry, 0, func(_ context.Context, v any) (any, error) {
		e := v.(*SitemapEntry)
		if e.Item != nil && e.Item.Type == "post" {
			e.Loc = "/blog/" + e.Item.Slug
		}
		return e, nil
	})
	out, _ = s.Sitemap(context.Background())
	if !strings.Contains(string(out), "https://site.example/blog/hello") {
		t.Errorf("filter not applied")
	}
}

// countingContent counts Query calls and returns endless published pages.
type countingContent struct {
	content.Service
	calls *int
}

func (c countingContent) Types(context.Context) ([]content.Type, error) {
	return []content.Type{{Slug: "post", Public: true}}, nil
}

func (c countingContent) Query(_ context.Context, q content.Query) (content.Page[content.Item], error) {
	*c.calls++
	items := make([]content.Item, q.PerPage)
	for i := range items {
		items[i] = content.Item{Type: "post", Status: "published", Slug: fmt.Sprintf("p-%d-%d", q.Page, i)}
	}
	return content.Page[content.Item]{Items: items, Total: 1_000_000, Page: q.Page, PerPage: q.PerPage}, nil
}

func TestSitemapStopsAtCap(t *testing.T) {
	s := testService(t)
	calls := 0
	s.content = func() (content.Service, bool) { return countingContent{calls: &calls}, true }
	out, err := s.Sitemap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "<url>"); n != sitemapMaxEntries {
		t.Fatalf("entries: %d", n)
	}
	if want := (sitemapMaxEntries + sitemapPageSize - 1) / sitemapPageSize; calls > want {
		t.Fatalf("kept paging after the cap: %d calls (want <= %d)", calls, want)
	}
}

func TestJSONLDKeepsBytesAndEscapes(t *testing.T) {
	got := jsonLD(` { "b": 1.10, "a": "</script>\u2028&" } `)
	if got != `{"b":1.10,"a":"\u003c/script\u003e\u2028\u0026"}` {
		t.Fatalf("jsonLD: %s", got)
	}
	if jsonLD(`"scalar"`) != "" || jsonLD(`{bad`) != "" || jsonLD(`null`) != "" {
		t.Fatal("non-object input must be dropped")
	}
}

func TestRobots(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if got := s.Robots(ctx); got != "User-agent: *\nAllow: /\nSitemap: http://config.example/sitemap.xml\n" {
		t.Fatalf("default (config base): %q", got)
	}
	set(t, s, SettingSiteBaseURL, "https://zad.example/")
	if got := s.Robots(ctx); got != "User-agent: *\nAllow: /\nSitemap: https://zad.example/sitemap.xml\n" {
		t.Fatalf("default (site base): %q", got)
	}
	set(t, s, SettingRobotsTxt, "User-agent: *\nDisallow: /private")
	if got := s.Robots(ctx); got != "User-agent: *\nDisallow: /private\n" {
		t.Fatalf("custom: %q", got)
	}
}
