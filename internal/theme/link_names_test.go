package theme

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// attr returns an element's attribute value and whether it is present.
func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// anchorHasName reports whether an <a> exposes a non-empty accessible name:
// a label attribute, visible text, or an image with non-empty alt/title.
// An element with aria-hidden="true" is out of the accessibility tree.
func anchorHasName(a *html.Node) bool {
	if v, _ := attr(a, "aria-label"); strings.TrimSpace(v) != "" {
		return true
	}
	if v, ok := attr(a, "aria-labelledby"); ok && strings.TrimSpace(v) != "" {
		return true
	}
	if v, _ := attr(a, "title"); strings.TrimSpace(v) != "" {
		return true
	}
	named := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if named {
			return
		}
		if v, _ := attr(n, "aria-hidden"); v == "true" {
			return
		}
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
			named = true
			return
		}
		if n.Type == html.ElementNode && (n.Data == "img" || n.Data == "svg") {
			alt, _ := attr(n, "alt")
			ttl, _ := attr(n, "title")
			lbl, _ := attr(n, "aria-label")
			if strings.TrimSpace(alt) != "" || strings.TrimSpace(ttl) != "" || strings.TrimSpace(lbl) != "" {
				named = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := a.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return named
}

// TestCatalogLinksHaveAccessibleNames enforces the media/theme contract rule
// that an image-only link must still carry an accessible name even when the
// image's alt is empty (decorative): a link with no label, no text and only
// empty-alt images is invisible to assistive tech. Decorative images belong
// in a link that is named (aria-label) or removed from the accessibility tree
// (aria-hidden on the link, as the default card does). Rendered with a
// featured image whose alt is empty — the exact case the rule guards.
func TestCatalogLinksHaveAccessibleNames(t *testing.T) {
	for _, slug := range []string{"default", "business", "magazine", "portfolio", "docs"} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			e.fake.Media.Add(media.Media{ID: "cover", URL: "/media/cover.jpg", Width: 1200, Height: 630, Alt: ""})
			post := e.fake.Content.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello world", Body: "<p>Body</p>",
				PublishedAt: "2026-10-10T00:00:00Z", FeaturedMediaID: "cover", Terms: map[string][]string{"category": {"news"}}})
			term := e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "news", Name: "News", Description: "All news"})
			list := func() *Context {
				return &Context{Items: []content.Item{post}, Pagination: NewPagination(1, 1, 3), Query: map[string]string{"q": "hel"}}
			}
			cases := []struct {
				name string
				req  kernel.KindRequest
				tc   *Context
			}{
				{"home", kernel.KindRequest{Kind: KindHome}, list()},
				{"archive", kernel.KindRequest{Kind: KindArchive, Type: "post"}, list()},
				{"single", kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post}},
				{"taxonomy", kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "news"}, func() *Context { c := list(); c.Term = &term; return c }()},
			}
			for _, c := range cases {
				rec, err := e.render(c.req, c.tc)
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				doc, err := html.Parse(strings.NewReader(rec.Body.String()))
				if err != nil {
					t.Fatalf("%s: parse: %v", c.name, err)
				}
				var walk func(*html.Node, bool)
				walk = func(n *html.Node, hidden bool) {
					if v, _ := attr(n, "aria-hidden"); v == "true" {
						hidden = true
					}
					if n.Type == html.ElementNode && n.Data == "a" {
						if _, ok := attr(n, "href"); ok && !hidden && !anchorHasName(n) {
							t.Errorf("%s/%s: link without an accessible name: %s", slug, c.name, render(n))
						}
					}
					for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
						walk(ch, hidden)
					}
				}
				walk(doc, false)
			}
		})
	}
}

// render serializes a node for a readable failure message.
func render(n *html.Node) string {
	var b strings.Builder
	_ = html.Render(&b, n)
	s := b.String()
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// TestAnchorNameRuleCatchesUnnamedImageLink is the negative control: the
// acceptance check must fail an empty-alt image-only link with no name, and
// pass the ways to make it valid (aria-label on the link, aria-hidden on the
// link, or a non-empty alt).
func TestAnchorNameRuleCatchesUnnamedImageLink(t *testing.T) {
	cases := []struct {
		name string
		frag string
		want bool // want an accessible name
	}{
		{"empty-alt image only", `<a href="/x"><img src="/c.jpg" alt=""></a>`, false},
		{"no-alt image only", `<a href="/x"><img src="/c.jpg"></a>`, false},
		{"aria-label on link", `<a href="/x" aria-label="Open"><img src="/c.jpg" alt=""></a>`, true},
		{"non-empty alt", `<a href="/x"><img src="/c.jpg" alt="A cover"></a>`, true},
		{"visible text", `<a href="/x"><img src="/c.jpg" alt=""> Read</a>`, true},
	}
	for _, c := range cases {
		doc, err := html.Parse(strings.NewReader(c.frag))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var a *html.Node
		var find func(*html.Node)
		find = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "a" {
				a = n
			}
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				find(ch)
			}
		}
		find(doc)
		if a == nil {
			t.Fatalf("%s: no anchor parsed", c.name)
		}
		if got := anchorHasName(a); got != c.want {
			t.Errorf("%s: anchorHasName = %v, want %v", c.name, got, c.want)
		}
	}
}
