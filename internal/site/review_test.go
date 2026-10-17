package site_test

import (
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestTermArchiveOnlyPublicTypes(t *testing.T) {
	e := boot(t)
	ctx := t.Context()
	e.fake.Content.SaveType(ctx, content.Type{Slug: "invoice", Public: false, Taxonomies: []string{"category"}})
	e.fake.Taxonomy.Save(ctx, taxonomy.Taxonomy{Slug: "category", Public: true, Hierarchical: true, Types: []string{"post", "invoice"}})
	e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "news", Name: "News"})
	e.fake.Content.Add(content.Item{Type: "post", Slug: "pub", Title: "Public post", PublishedAt: "2026-10-01T00:00:00Z", Terms: map[string][]string{"category": {"news"}}})
	e.fake.Content.Add(content.Item{Type: "invoice", Slug: "inv", Title: "Private invoice", PublishedAt: "2026-10-02T00:00:00Z", Terms: map[string][]string{"category": {"news"}}})
	resp, body := e.get("/category/news")
	if resp.StatusCode != 200 {
		t.Fatalf("term: %d", resp.StatusCode)
	}
	mustContain(t, body, "Public post")
	mustNotContain(t, body, "Private invoice")
}

func TestHeadRequests(t *testing.T) {
	e := boot(t)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "about", Title: "About"})
	for _, p := range []string{"/", "/about", "/search?q=x", "/robots.txt", "/themes/default/assets/style.css"} {
		get, _ := e.get(p)
		head, body := e.do("HEAD", p, nil)
		if head.StatusCode != 200 || body != "" || head.Header.Get("Content-Type") != get.Header.Get("Content-Type") {
			t.Fatalf("HEAD %s: %d %q %q vs %q", p, head.StatusCode, body, head.Header.Get("Content-Type"), get.Header.Get("Content-Type"))
		}
	}
	if resp, _ := e.do("HEAD", "/nope", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HEAD 404: %d", resp.StatusCode)
	}
}

// GetBySlug semantics: exact site locale first, then items stored without a
// locale; never another locale.
func TestSlugLookupLocale(t *testing.T) {
	e := boot(t)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "ar-page", Title: "Arabic locale page", Locale: "ar"})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "plain", Title: "No locale page"})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "en-page", Title: "English only page", Locale: "en"})
	for path, code := range map[string]int{"/ar-page": 200, "/plain": 200, "/en-page": 404} {
		if resp, _ := e.get(path); resp.StatusCode != code {
			t.Fatalf("%s: %d want %d", path, resp.StatusCode, code)
		}
	}
	e.setting("site.locale", "en")
	if resp, _ := e.get("/en-page"); resp.StatusCode != 200 {
		t.Fatalf("en site: %d", resp.StatusCode)
	}
	if resp, _ := e.get("/ar-page"); resp.StatusCode != 404 {
		t.Fatalf("ar page on en site: %d", resp.StatusCode)
	}
}

func TestChildOfDraftPageHidden(t *testing.T) {
	e := boot(t)
	parent := e.fake.Content.Add(content.Item{Type: "page", Slug: "hidden", Title: "Hidden parent", Status: "draft"})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "kid", Title: "Published kid", ParentID: parent.ID})
	if resp, _ := e.get("/hidden/kid"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	e.loginAdmin()
	if resp, _ := e.get("/hidden/kid"); resp.StatusCode != 200 {
		t.Fatalf("admin: %d", resp.StatusCode)
	}
}
