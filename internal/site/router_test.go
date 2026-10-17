package site_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestRouterHierarchicalPageSlug(t *testing.T) {
	e := boot(t)
	fc := e.fake.Content
	about := fc.Add(content.Item{Type: "page", Slug: "about", Title: "About us", Body: "<p>About body</p>"})
	team := fc.Add(content.Item{Type: "page", Slug: "team", Title: "Our team", ParentID: about.ID})
	fc.Add(content.Item{Type: "page", Slug: "lead", Title: "Team lead", ParentID: team.ID})
	fc.Add(content.Item{Type: "page", Slug: "مرحبا", Title: "Arabic page"})
	fc.Add(content.Item{Type: "page", Slug: "secret", Title: "Hidden draft", Status: "draft"})

	for path, want := range map[string]string{
		"/about":                          "About us",
		"/about/":                         "About us",
		"/about/team":                     "Our team",
		"/about/team/lead":                "Team lead",
		"/%D9%85%D8%B1%D8%AD%D8%A8%D8%A7": "Arabic page",
	} {
		resp, body := e.get(path)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		mustContain(t, body, want)
	}
	_, body := e.get("/about")
	mustContain(t, body, "<p>About body</p>") // sanitized body rendered unescaped
	for _, path := range []string{"/team", "/lead", "/about/lead", "/team/lead", "/x/about/team", "/about/team/lead/more", "/secret"} {
		if resp, _ := e.get(path); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
	// Drafts are previewable by users with content.view.
	e.loginAdmin()
	resp, body := e.get("/secret")
	if resp.StatusCode != 200 {
		t.Fatalf("admin preview: %d", resp.StatusCode)
	}
	mustContain(t, body, "Hidden draft")
}

func TestRouter404UsesTheme(t *testing.T) {
	e := boot(t)
	e.fake.Redirects.Save(t.Context(), redirects.Redirect{FromPath: "/old-path", ToURL: "/new-path", Status: 301, Enabled: true})
	for _, path := range []string{"/nope", "/post/missing", "/category/missing", "/a/b/c/d"} {
		resp, body := e.get(path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		mustContain(t, body, `data-theme="default"`, `data-kind="404"`, `dir="rtl"`, "الصفحة غير موجودة")
	}
	resp, _ := e.get("/old-path")
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/new-path" {
		t.Fatalf("redirect before 404: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestPublicPagesUseSiteLocale(t *testing.T) {
	e := boot(t)
	_, body := e.do("GET", "/", nil, "Accept-Language", "en")
	mustContain(t, body, `lang="ar"`, `dir="rtl"`)
	e.setting("site.locale", "en")
	_, body = e.get("/")
	mustContain(t, body, `lang="en"`, `dir="ltr"`)
}

func TestSearchRoute(t *testing.T) {
	e := boot(t)
	e.fake.Content.Add(content.Item{Type: "post", Slug: "go", Title: "Go tips", Body: "<p>golang</p>", PublishedAt: "2026-10-01T00:00:00Z"})
	e.fake.Content.Add(content.Item{Type: "post", Slug: "cook", Title: "Cooking", PublishedAt: "2026-10-02T00:00:00Z"})
	e.fake.Content.Add(content.Item{Type: "post", Slug: "gd", Title: "Go draft", Status: "draft"})
	resp, body := e.get("/search?q=go")
	if resp.StatusCode != 200 {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="search"`, "Go tips", `value="go"`, "noindex")
	mustNotContain(t, body, "Cooking", "Go draft")

	_, body = e.get("/search?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E")
	mustNotContain(t, body, "<script>alert(1)</script>")
	mustContain(t, body, "&lt;script&gt;")

	resp, body = e.get("/search")
	if resp.StatusCode != 200 {
		t.Fatalf("empty search: %d", resp.StatusCode)
	}
	mustContain(t, body, `name="q"`)
	mustNotContain(t, body, "Go tips")
}

func TestHomeArchiveTaxonomySingle(t *testing.T) {
	e := boot(t)
	fc := e.fake.Content
	for i := 1; i <= 12; i++ {
		fc.Add(content.Item{Type: "post", Slug: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("Post number %d", i),
			PublishedAt: fmt.Sprintf("2026-09-%02dT10:00:00Z", i), Terms: map[string][]string{"category": {"news"}}})
	}
	hello := fc.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello world", Body: "<p>Hi</p>", PublishedAt: "2026-10-10T10:00:00Z",
		Terms: map[string][]string{"category": {"news"}}, FeaturedMediaID: "m1"})
	e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "news", Name: "News"})
	e.fake.Media.Add(media.Media{ID: "m1", URL: "/media/x.jpg", Width: 800, Height: 400, Alt: "cover"})
	e.fake.Menus.Save(t.Context(), menus.Menu{Slug: "main", Location: "primary", Items: []menus.Item{{Label: "Blog link", Kind: "archive", RefID: "post"}}})

	resp, body := e.get("/")
	if resp.StatusCode != 200 {
		t.Fatalf("home: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="home"`, "Hello world", `href="http://example.test/post/hello"`, `href="http://example.test/blog/"`, "Blog link", "?page=2")
	mustNotContain(t, body, "Post number 3<")
	_, body = e.get("/?page=2")
	mustContain(t, body, "Post number 3")

	resp, body = e.get("/blog/")
	if resp.StatusCode != 200 {
		t.Fatalf("archive: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="archive"`, "Hello world")
	if resp, _ := e.get("/blog"); resp.StatusCode != 200 {
		t.Fatalf("archive without slash: %d", resp.StatusCode)
	}

	resp, body = e.get("/category/news")
	if resp.StatusCode != 200 {
		t.Fatalf("taxonomy: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="taxonomy"`, "News", "Hello world")

	resp, body = e.get("/post/hello")
	if resp.StatusCode != 200 {
		t.Fatalf("single: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="single"`, "Hello world", "<p>Hi</p>", `alt="cover"`, "10 أكتوبر 2026", `href="http://example.test/category/news"`)

	// Static front page.
	front := fc.Add(content.Item{Type: "page", Slug: "welcome", Title: "Welcome page"})
	e.setting("site.front_page", front.ID)
	_, body = e.get("/")
	mustContain(t, body, "Welcome page", `data-kind="page"`)
	_ = hello

	// sitemap.xml and robots.txt delegate to seo.Service.
	resp, body = e.get("/sitemap.xml")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "xml") || !strings.Contains(body, "<urlset") {
		t.Fatalf("sitemap: %d %s", resp.StatusCode, body)
	}
	resp, body = e.get("/robots.txt")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || !strings.Contains(body, "User-agent") {
		t.Fatalf("robots: %d %s", resp.StatusCode, body)
	}

	// Theme assets: cached, no traversal, no listing.
	resp, body = e.get("/themes/default/assets/style.css")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "public, max-age=3600" || !strings.Contains(body, "--green") {
		t.Fatalf("asset: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	for _, p := range []string{"/themes/default/assets/../theme.json", "/themes/default/assets/%2e%2e/theme.json", "/themes/default/assets/", "/themes/nope/assets/style.css", "/themes/default/theme.json"} {
		if resp, _ := e.get(p); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}
