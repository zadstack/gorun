package site_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// A translated category: each locale's archive lives under its own prefix and slug, lists the
// items of that locale tagged with any term of the group, announces the translations as
// alternates, and the wrong slug for a locale redirects to that locale's translation.
func TestTermArchivesPerLocale(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ts, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	akhbar, err := ts.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Name: "أخبار", Slug: "akhbar", Locale: "ar"})
	if err != nil {
		t.Fatal(err)
	}
	news, err := ts.CreateTermTranslation(ctx, akhbar.ID, taxonomy.Term{Locale: "en", Name: "News", Slug: "news"})
	if err != nil {
		t.Fatal(err)
	}
	ar := publish(t, cs, content.Input{Type: "post", Slug: "khabar", Title: "خبر عربي", Locale: "ar", Terms: map[string][]string{"category": {"akhbar"}}})
	// The English copy keeps the Arabic term (CreateTranslation copies terms): the English archive
	// still lists it because both terms are one group.
	translate(t, cs, ar, "English story", true)
	publish(t, cs, content.Input{Type: "post", Slug: "tagged-en", Title: "Tagged in English", Locale: "en", Terms: map[string][]string{"category": {"news"}}})

	resp, body := e.get("/category/akhbar")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ar archive: %d", resp.StatusCode)
	}
	mustContain(t, body, "خبر عربي", `hreflang="en" href="http://example.test/en/category/news"`, `hreflang="ar" href="http://example.test/category/akhbar"`)
	mustNotContain(t, body, "English story", "Tagged in English")

	resp, body = e.get("/en/category/news")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("en archive: %d", resp.StatusCode)
	}
	mustContain(t, body, "English story", "Tagged in English", "News")
	mustNotContain(t, body, "خبر عربي")

	// The Arabic slug under /en/ and the English slug without a prefix redirect to the translation.
	if resp, _ = e.get("/en/category/akhbar?page=2"); resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/en/category/news?page=2" {
		t.Fatalf("en prefix, ar slug: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = e.get("/category/news"); resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/category/akhbar" {
		t.Fatalf("site locale, en slug: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Theme links of the English item point at the English term.
	_, body = e.get("/en/post/khabar")
	mustContain(t, body, "/en/category/news")
	mustNotContain(t, body, "/en/category/akhbar")

	// A menu item naming the English term links to it under /en/.
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	if _, err := ms.Save(ctx, menus.Menu{Slug: "main-en", Name: "Main EN", Location: "primary", Locale: "en",
		Items: []menus.Item{{Kind: menus.KindTerm, Label: "NEWS-LINK", RefID: news.ID}}}); err != nil {
		t.Fatal(err)
	}
	_, body = e.get("/en/")
	mustContain(t, body, `href="http://example.test/en/category/news">NEWS-LINK`)

	// The sitemap lists each term under its own locale.
	_, xml := e.get("/sitemap.xml")
	mustContain(t, xml, "<loc>http://example.test/category/akhbar</loc>", "<loc>http://example.test/en/category/news</loc>")
	mustNotContain(t, xml, "<loc>http://example.test/category/news</loc>")
}

// An untranslated term keeps today's behaviour: one slug, served under every locale prefix.
func TestUntranslatedTermServesEveryLocale(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ts, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	if _, err := ts.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Name: "عام", Slug: "general"}); err != nil {
		t.Fatal(err)
	}
	publish(t, cs, content.Input{Type: "post", Slug: "en-general", Title: "General English", Locale: "en", Terms: map[string][]string{"category": {"general"}}})
	resp, body := e.get("/en/category/general")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("untranslated term under /en/: %d", resp.StatusCode)
	}
	mustContain(t, body, "General English")
}
