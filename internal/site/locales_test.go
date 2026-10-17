package site_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

// translate publishes an English translation of src titled title.
func translate(t *testing.T, cs content.Service, src *content.Item, title string, live bool) *content.Item {
	t.Helper()
	ctx := context.Background()
	tr, err := cs.CreateTranslation(ctx, src.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	in := content.InputFromItem(tr)
	in.Title = title
	if tr, err = cs.Update(ctx, tr.ID, in); err != nil {
		t.Fatal(err)
	}
	if live {
		if err := cs.Publish(ctx, tr.ID); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}

// The site locale is unprefixed and another supported locale lives under /<locale>/: each URL
// serves its own locale's item, lists show only their locale, and the site locale's prefix
// redirects to the unprefixed URL.
func TestLocalePrefixRouting(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ar := publish(t, cs, content.Input{Type: "post", Slug: "hello", Title: "مرحبا بالعالم", Locale: "ar"})
	translate(t, cs, ar, "Hello Planet", true)
	publish(t, cs, content.Input{Type: "post", Slug: "only-ar", Title: "عربي فقط", Locale: "ar"})
	publish(t, cs, content.Input{Type: "page", Slug: "english", Title: "صفحة إنجليزي", Locale: "ar"})

	resp, body := e.get("/post/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ar single: %d", resp.StatusCode)
	}
	mustContain(t, body, "مرحبا بالعالم", `lang="ar"`, `dir="rtl"`)
	mustNotContain(t, body, "Hello Planet")

	resp, body = e.get("/en/post/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("en single: %d\n%s", resp.StatusCode, body)
	}
	mustContain(t, body, "Hello Planet", `lang="en"`, `dir="ltr"`, `<link rel="canonical" href="http://example.test/en/post/hello">`)
	mustNotContain(t, body, "مرحبا بالعالم")

	if resp, _ = e.get("/ar/post/hello?x=1"); resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/post/hello?x=1" {
		t.Fatalf("site locale prefix: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = e.get("/en/post/only-ar"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no translation: %d", resp.StatusCode)
	}
	if resp, _ = e.get("/english"); resp.StatusCode != http.StatusOK {
		t.Fatalf("a slug starting with a locale code is not a prefix: %d", resp.StatusCode)
	}

	_, home := e.get("/")
	mustContain(t, home, "مرحبا بالعالم", "عربي فقط")
	mustNotContain(t, home, "Hello Planet")
	resp, enHome := e.get("/en/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("en home: %d", resp.StatusCode)
	}
	mustContain(t, enHome, "Hello Planet", `lang="en"`)
	mustNotContain(t, enHome, "مرحبا بالعالم", "عربي فقط")

	_, found := e.get("/en/search?q=Planet")
	mustContain(t, found, "Hello Planet")
	_, found = e.get("/search?q=Planet")
	mustNotContain(t, found, "Hello Planet")

	_, xml := e.get("/sitemap.xml")
	mustContain(t, xml, "<loc>http://example.test/post/hello</loc>", "<loc>http://example.test/en/post/hello</loc>")
}

// Published translations are announced with hreflang alternates (and x-default) and a language
// link; drafts never are.
func TestAlternatesListPublishedTranslations(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ar := publish(t, cs, content.Input{Type: "post", Slug: "hello", Title: "مرحبا", Locale: "ar"})
	translate(t, cs, ar, "Hello", true)
	draft := publish(t, cs, content.Input{Type: "post", Slug: "later", Title: "لاحقًا", Locale: "ar"})
	translate(t, cs, draft, "Later", false)

	_, body := e.get("/post/hello")
	mustContain(t, body, `<link rel="alternate" hreflang="ar" href="http://example.test/post/hello">`,
		`<link rel="alternate" hreflang="en" href="http://example.test/en/post/hello">`,
		`<link rel="alternate" hreflang="x-default" href="http://example.test/post/hello">`,
		`href="http://example.test/en/post/hello" hreflang="en" lang="en"`)
	_, body = e.get("/post/later")
	mustNotContain(t, body, `hreflang="en"`, "/en/post/later")
	// list pages offer the same path in each locale
	_, body = e.get("/blog/")
	mustContain(t, body, `<link rel="alternate" hreflang="en" href="http://example.test/en/blog/">`)
}

var hrefRe = regexp.MustCompile(`href="(?:http://example\.test)?(/[^"#]*)"`)

// Every internal link on the English pages answers (other modules' routes are not prefixed).
func TestLocalizedPagesHaveNoDeadLinks(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ar := publish(t, cs, content.Input{Type: "post", Slug: "hello", Title: "مرحبا", Locale: "ar"})
	translate(t, cs, ar, "Hello Planet", true)
	only := publish(t, cs, content.Input{Type: "post", Slug: "only-ar", Title: "عربي فقط", Locale: "ar"})
	// menus are not per locale: their links keep pointing where they were saved
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	m, err := ms.Save(context.Background(), menus.Menu{Slug: "main", Name: "Main", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.SaveItems(context.Background(), m.ID, []menus.Item{{Kind: menus.KindContent, RefID: only.ID},
		{Kind: menus.KindCustom, Label: "Custom", URL: "/blog/"}}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, page := range []string{"/en/", "/en/post/hello", "/en/search?q=Planet", "/en/blog/"} {
		resp, body := e.get(page)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", page, resp.StatusCode)
		}
		for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
			link := strings.ReplaceAll(m[1], "&amp;", "&")
			if seen[link] || strings.HasPrefix(link, "//") {
				continue
			}
			seen[link] = true
			if resp, _ := e.get(link); resp.StatusCode == http.StatusNotFound {
				t.Errorf("%s links to %s: 404", page, link)
			}
		}
	}
}

// /en/ pages use the English menu of a location when there is one; the site's own pages keep
// the menu for every locale.
func TestLocaleMenus(t *testing.T) {
	e := bootReal(t)
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	ctx := context.Background()
	for _, m := range []menus.Menu{
		{Slug: "main", Name: "Main", Location: "primary", Items: []menus.Item{{Kind: menus.KindCustom, Label: "ALL-MENU", URL: "/blog/"}}},
		{Slug: "main-en", Name: "Main EN", Location: "primary", Locale: "en", Items: []menus.Item{{Kind: menus.KindCustom, Label: "EN-MENU", URL: "/en/blog/"}}},
	} {
		if _, err := ms.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, home := e.get("/")
	mustContain(t, home, "ALL-MENU")
	mustNotContain(t, home, "EN-MENU")
	_, en := e.get("/en/")
	mustContain(t, en, "EN-MENU")
	mustNotContain(t, en, "ALL-MENU")
}
