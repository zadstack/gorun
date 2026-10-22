package theme

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// Realistic fixtures: a long post with a long title and no featured image,
// and an archive page of twelve items where only half carry an image.
func longPost(locale string) *content.Item {
	word := "Infrastructure"
	title := strings.Repeat("A deliberately long headline that wraps on narrow screens ", 3)
	if locale == "ar" {
		word = "البنية"
		title = strings.Repeat("عنوان طويل جدًا يلتف على الشاشات الضيقة ويختبر الأسطر ", 3)
	}
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("<p>")
		for j := 0; j < 50; j++ {
			b.WriteString(word + " ")
		}
		b.WriteString("</p>")
		if i%10 == 0 {
			b.WriteString("<h2>" + word + "</h2>")
		}
	}
	return &content.Item{ID: "long", Type: "post", Slug: "long", Title: string([]rune(strings.TrimSpace(title))[:120]), Body: b.String(), Status: "published", Locale: locale, PublishedAt: "2026-10-01T10:00:00Z"}
}

func archiveItems(locale string) []content.Item {
	out := make([]content.Item, 0, 12)
	for i := 0; i < 12; i++ {
		it := content.Item{ID: fmt.Sprintf("a%d", i), Type: "post", Slug: fmt.Sprintf("a-%d", i), Title: fmt.Sprintf("Archive item %d", i), Status: "published", Locale: locale, PublishedAt: "2026-09-01T10:00:00Z", Excerpt: "Short summary."}
		if i%2 == 0 {
			it.FeaturedMediaID = "img" + fmt.Sprint(i)
		}
		out = append(out, it)
	}
	return out
}

var (
	imgTag   = regexp.MustCompile(`<img\b[^>]*>`)
	h1Tag    = regexp.MustCompile(`<h1\b`)
	emptySrc = regexp.MustCompile(`src=""`)
)

func checkVariantPage(t *testing.T, body, slot, want, locale string) {
	t.Helper()
	mustContain(t, body, `data-variant-`+slot+`="`+want+`"`, `id="main"`, `href="#main"`, `lang="`+locale+`"`)
	dir := "ltr"
	if locale == "ar" {
		dir = "rtl"
	}
	mustContain(t, body, `dir="`+dir+`"`)
	if n := len(h1Tag.FindAllString(body, -1)); n != 1 {
		t.Errorf("%s/%s: %d <h1> elements", slot, want, n)
	}
	for _, img := range imgTag.FindAllString(body, -1) {
		if !strings.Contains(img, "alt=") {
			t.Errorf("%s/%s: <img> without alt: %s", slot, want, img)
		}
		if emptySrc.MatchString(img) {
			t.Errorf("%s/%s: <img> with empty src: %s", slot, want, img)
		}
	}
}

// Every catalog theme renders each declared single/archive variant in both
// locales with long content and missing images, and an undeclared value
// falls back to native.
func TestCatalogThemesRenderDeclaredVariants(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"default", "business", "magazine", "portfolio"} {
		t.Run(slug, func(t *testing.T) {
			e := bootRealTheme(t, root)
			if err := e.svc.Activate(context.Background(), slug); err != nil {
				t.Fatal(err)
			}
			info := e.svc.Active(context.Background())
			for _, slot := range []string{"single", "archive"} {
				declared := DeclaredVariants(info, slot)
				if len(declared) < 2 {
					t.Fatalf("%s declares no %s variants", slug, slot)
				}
				for _, v := range append(declared, "bogus") {
					want := ResolveVariant(info, slot, v)
					e.setting(SettingKey(slug, slot+"_style"), v)
					for _, locale := range []string{"ar", "en"} {
						var req kernel.KindRequest
						tc := &Context{Locale: locale}
						if slot == "single" {
							req = kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "long"}
							tc.Content = longPost(locale)
						} else {
							req = kernel.KindRequest{Kind: KindArchive, Type: "post"}
							tc.Items = archiveItems(locale)
							tc.Pagination = Pagination{Page: 1, PerPage: 12, Total: 24, Pages: 2, HasNext: true}
						}
						rec, err := e.render(req, tc)
						if err != nil || rec.Code != 200 {
							t.Fatalf("%s %s=%s %s: %v %d", slug, slot, v, locale, err, rec.Code)
						}
						body := rec.Body.String()
						checkVariantPage(t, body, slot, want, locale)
						if slot == "archive" {
							mustContain(t, body, "Archive item 11")
						} else {
							mustContain(t, body, "variants.css")
						}
					}
				}
			}
		})
	}
}

// The docs theme declares no single/archive variants: stored values resolve
// to native and the inherited templates still render.
func TestDocsThemeFallsBackToNativeVariants(t *testing.T) {
	root, _ := filepath.Abs("../../themes")
	e := bootRealTheme(t, root)
	if err := e.svc.Activate(context.Background(), "docs"); err != nil {
		t.Fatal(err)
	}
	info := e.svc.Active(context.Background())
	for _, slot := range []string{"single", "archive"} {
		if got := DeclaredVariants(info, slot); len(got) != 1 {
			t.Fatalf("docs %s declares %v", slot, got)
		}
	}
	e.setting("theme.docs.single_style", "wide")
	rec, err := e.render(kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "long"}, &Context{Locale: "en", Content: longPost("en")})
	if err != nil || rec.Code != 200 {
		t.Fatal(err, rec.Code)
	}
	mustContain(t, rec.Body.String(), `data-variant-single="native"`)
}

// archive_style governs taxonomy pages and search results too, and an
// empty result keeps the theme's own empty state under every variant.
func TestArchiveVariantAppliesToTaxonomySearchAndEmptyLists(t *testing.T) {
	root, _ := filepath.Abs("../../themes")
	for _, slug := range []string{"default", "business", "magazine", "portfolio"} {
		t.Run(slug, func(t *testing.T) {
			e := bootRealTheme(t, root)
			if err := e.svc.Activate(context.Background(), slug); err != nil {
				t.Fatal(err)
			}
			info := e.svc.Active(context.Background())
			term := &taxonomy.Term{ID: "category:news", Taxonomy: "category", Slug: "news", Name: "News"}
			for _, v := range DeclaredVariants(info, "archive")[1:] {
				e.setting(SettingKey(slug, "archive_style"), v)
				for _, kind := range []struct {
					req kernel.KindRequest
					tc  func() *Context
				}{
					{kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "news"}, func() *Context { return &Context{Locale: "en", Term: term, Items: archiveItems("en")} }},
					{kernel.KindRequest{Kind: KindSearch}, func() *Context {
						return &Context{Locale: "en", Items: archiveItems("en"), Query: map[string]string{"q": "Archive"}}
					}},
				} {
					rec, err := e.render(kind.req, kind.tc())
					if err != nil || rec.Code != 200 {
						t.Fatalf("%s %s=%s: %v %d", slug, kind.req.Kind, v, err, rec.Code)
					}
					mustContain(t, rec.Body.String(), `data-variant-archive="`+v+`"`, "Archive item 11", "variants.css")
					if v == "grid" {
						mustContain(t, rec.Body.String(), "gr-card-grid")
					}
					// Empty results: the theme's own empty state, no grid or rows.
					empty := kind.tc()
					empty.Items = nil
					rec, err = e.render(kind.req, empty)
					if err != nil || rec.Code != 200 {
						t.Fatalf("%s empty %s=%s: %v %d", slug, kind.req.Kind, v, err, rec.Code)
					}
					mustNotContain(t, rec.Body.String(), "gr-card-grid", "gr-list-compact")
					body := rec.Body.String()
					if !strings.Contains(body, "Nothing published yet") && !strings.Contains(body, "Nothing found") && !strings.Contains(body, "Published articles will appear here") {
						t.Errorf("%s empty %s=%s: no empty state in body", slug, kind.req.Kind, v)
					}
				}
			}
		})
	}
}
