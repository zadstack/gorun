package theme

import (
	"errors"
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

func TestResolveSingleHierarchy(t *testing.T) {
	reg := kernel.NewThemeRegistry()
	reg.AddKind("product", func(r kernel.KindRequest) []string { return []string{"product-" + r.Slug + ".html", "product.html"} })
	cases := []struct {
		name string
		req  kernel.KindRequest
		want []string
	}{
		{"single", kernel.KindRequest{Kind: "single", Type: "post", Slug: "hello"},
			[]string{"single-post-hello.html", "single-post.html", "single.html", "index.html"}},
		{"single arabic slug", kernel.KindRequest{Kind: "single", Type: "post", Slug: "مرحبا"},
			[]string{"single-post-مرحبا.html", "single-post.html", "single.html", "index.html"}},
		{"page", kernel.KindRequest{Kind: "page", Type: "page", Slug: "about", Template: "wide"},
			[]string{"page-about.html", "page-wide.html", "page.html", "single.html", "index.html"}},
		{"page template given as file name", kernel.KindRequest{Kind: "page", Slug: "about", Template: "page-wide.html"},
			[]string{"page-about.html", "page-wide.html", "page.html", "single.html", "index.html"}},
		{"page without template", kernel.KindRequest{Kind: "page", Slug: "about"},
			[]string{"page-about.html", "page.html", "single.html", "index.html"}},
		{"home", kernel.KindRequest{Kind: "home"}, []string{"front-page.html", "home.html", "index.html"}},
		{"archive", kernel.KindRequest{Kind: "archive", Type: "post"}, []string{"archive-post.html", "archive.html", "index.html"}},
		{"taxonomy", kernel.KindRequest{Kind: "taxonomy", Taxonomy: "category", Term: "news"},
			[]string{"taxonomy-category-news.html", "taxonomy-category.html", "taxonomy.html", "archive.html", "index.html"}},
		{"search", kernel.KindRequest{Kind: "search"}, []string{"search.html", "archive.html"}},
		{"404", kernel.KindRequest{Kind: "404"}, []string{"404.html", "index.html"}},
		{"module kind", kernel.KindRequest{Kind: "product", Slug: "pro"}, []string{"product-pro.html", "product.html", "index.html"}},
		{"unknown kind", kernel.KindRequest{Kind: "nope"}, []string{"index.html"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := candidates(c.req, reg); !slices.Equal(got, c.want) {
				t.Fatalf("candidates = %q, want %q", got, c.want)
			}
		})
	}

	have := map[string]bool{"single.html": true, "index.html": true, "archive.html": true}
	exists := func(n string) bool { return have[n] }
	if got, _ := resolve(kernel.KindRequest{Kind: "single", Type: "post", Slug: "x"}, reg, exists); got != "single.html" {
		t.Fatalf("resolve single = %q", got)
	}
	if got, _ := resolve(kernel.KindRequest{Kind: "search"}, reg, exists); got != "archive.html" {
		t.Fatalf("resolve search = %q", got)
	}
	if _, err := resolve(kernel.KindRequest{Kind: "search"}, reg, func(string) bool { return false }); !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("resolve with nothing: %v", err)
	}
}
