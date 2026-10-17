package theme

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme/fake"
)

// sitemapSEO is the fake SEO with a fixed sitemap.
type sitemapSEO struct {
	fake.SEO
	entries []seo.SitemapEntry
}

func (s sitemapSEO) Entries(context.Context) ([]seo.SitemapEntry, error) { return s.entries, nil }

func TestAdminSiteOverview(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", baseTheme(map[string]string{
		"assets/style.css": ":root {\n  --bg: #f3f5f2;\n  --green: #167252;\n  --font: \"Tajawal\", system-ui;\n  --radius: 6px;\n}\n.x{--not-root: #000;}",
	}))
	writeTheme(t, root, "child", `{"name":"Child Theme","version":"2.1.0","parent":"default"}`,
		map[string]string{"assets/theme.css": ":root{--green:#0d7c74;--navy:rgb(16, 35, 61)}", "screenshot.png": "\x89PNG\r\n\x1a\nfake"})
	e := newEnv(t, root)
	e.setting("site.theme", "child")
	e.setting("site.title", "زاد ستاك")
	e.setting("site.tagline", "منصات MCP")
	base := "https://example.com"
	entries := []seo.SitemapEntry{{Loc: base + "/"}}
	for i := range 30 {
		entries = append(entries, seo.SitemapEntry{Loc: fmt.Sprintf("%s/blog/p%d", base, i), Item: &content.Item{Type: "post", Title: fmt.Sprintf("مقال %d", i)}})
	}
	entries = append(entries,
		seo.SitemapEntry{Loc: base + "/about", Item: &content.Item{Type: "page", Title: "من نحن"}},
		seo.SitemapEntry{Loc: base + "/blog", Type: &content.Type{Slug: "post"}},
		seo.SitemapEntry{Loc: base + "/category/news", Term: &taxonomy.Term{Taxonomy: "category", Name: "أخبار"}})
	e.app.Deps.Services.Set(kernel.ServiceSEO, seo.Service(sitemapSEO{entries: entries}))

	c := e.client()
	if resp, _ := c.get("/admin/appearance/overview"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	e.login(c, "admin@example.com", "admin")
	resp, page := c.get("/admin/appearance/overview")
	if resp.StatusCode != 200 {
		t.Fatalf("overview: %d", resp.StatusCode)
	}
	mustContain(t, page,
		"زاد ستاك", "منصات MCP", "Child Theme", `sandbox=""`, `referrerpolicy="no-referrer"`, `/static/admin/site-overview.css`, `/static/admin/site-overview.js`,
		// Tokens: parent's, with the child's --green winning; not outside :root.
		"--bg", "#f3f5f2", "#0d7c74", "rgb(16, 35, 61)", "Tajawal", "--radius",
		// Pages: home, a capped group with the rest counted, archives, terms, checks.
		`data-src="https://example.com/"`, `data-src="https://example.com/about"`, "من نحن", "مقال 23",
		`data-src="https://example.com/blog"`, `data-src="https://example.com/category/news"`, "أخبار",
		`data-src="https://example.com/search?q=a"`, `data-src="https://example.com/gp-404-reference"`,
		`loading="lazy"`, ">36<",
	)
	if strings.Count(page, "<iframe ") != strings.Count(page, `sandbox="" referrerpolicy="no-referrer"`) {
		t.Fatal("every public preview must be isolated and omit the referrer")
	}
	srcAttribute := regexp.MustCompile(`\ssrc=`)
	for _, frame := range regexp.MustCompile(`<iframe\b[^>]*>`).FindAllString(page, -1) {
		if srcAttribute.MatchString(frame) {
			t.Fatal("overview starts every public preview before its card is near the viewport")
		}
	}
	if strings.Contains(page, "/admin/appearance/themes/child/screenshot.png") {
		t.Fatal("stale static theme thumbnail presented as current site identity")
	}
	if strings.Contains(page, "#167252") || strings.Contains(page, "--not-root") || strings.Contains(page, "مقال 24") {
		t.Fatal("overridden token, a non-:root variable or a page past the cap is shown")
	}
	if !strings.Contains(page, "و6 صفحة أخرى") && !strings.Contains(page, "and 6 more") {
		t.Fatalf("the rest of the capped group is not counted")
	}
	_, nav := c.get("/admin/appearance/themes")
	mustContain(t, nav, `href="/admin/appearance/overview"`)
	e.app.Deps.Services.Set(kernel.ServiceSEO, seo.Service(sitemapSEO{entries: []seo.SitemapEntry{
		{Loc: "javascript:alert(1)", Item: &content.Item{Type: "page", Title: "Unsafe sitemap URL"}},
	}}))
	_, unsafe := c.get("/admin/appearance/overview")
	mustContain(t, unsafe, `data-src="#ZgotmplZ"`)
	if strings.Contains(unsafe, `data-src="javascript:`) {
		t.Fatal("deferred preview bypasses template URL scheme filtering")
	}

	c2 := e.client()
	e.login(c2, "editor@example.com", "editor")
	if resp, _ := c2.get("/admin/appearance/overview"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}
