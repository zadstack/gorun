package theme

import (
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// TestChildThemesRenderAllKinds renders every kind with each bundled child of
// themes/default that uses its theme.css layer, with and without their theme
// settings filled in. business, magazine and portfolio are art-directed and
// tested on their own; minimal is retired into default (retired_test.go).
func TestChildThemesRenderAllKinds(t *testing.T) {
	themes := map[string]map[string]string{
		"docs": {"home_intro": "Read the guide"},
	}
	for slug, settings := range themes {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			post := e.fake.Content.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello", Body: "<p>Body</p>", PublishedAt: "2026-10-10T00:00:00Z",
				Terms: map[string][]string{"category": {"news"}}})
			second := e.fake.Content.Add(content.Item{Type: "post", Slug: "second", Title: "Second", Body: "<p>Two</p>", PublishedAt: "2026-10-09T00:00:00Z"})
			page := e.fake.Content.Add(content.Item{Type: "page", Slug: "guide", Title: "Guide", Body: "<p>Guide</p>", Status: "published"})
			term := e.fake.Taxonomy.AddTerm(taxonomy.Term{Taxonomy: "category", Slug: "news", Name: "News"})
			list := func() *Context {
				return &Context{Items: []content.Item{post, second}, Pagination: NewPagination(1, 2, 3), Query: map[string]string{"q": "hel"}}
			}
			cases := []struct {
				req kernel.KindRequest
				tc  *Context
			}{
				{kernel.KindRequest{Kind: KindHome}, list()},
				{kernel.KindRequest{Kind: KindHome}, &Context{}},
				{kernel.KindRequest{Kind: KindSingle, Type: "post", Slug: "hello"}, &Context{Content: &post}},
				{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "guide"}, &Context{Content: &page}},
				{kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact"}, &Context{Content: &page, Extra: map[string]any{"form_flash": "ok"}}},
				{kernel.KindRequest{Kind: KindArchive, Type: "post"}, list()},
				{kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "news"}, func() *Context { c := list(); c.Term = &term; return c }()},
				{kernel.KindRequest{Kind: KindSearch}, list()},
				{kernel.KindRequest{Kind: Kind404}, &Context{Status: http.StatusNotFound}},
			}
			for pass := 0; pass < 2; pass++ {
				if pass == 1 {
					for k, v := range settings {
						e.setting("theme."+slug+"."+k, v)
					}
				}
				for _, c := range cases {
					rec, err := e.render(c.req, c.tc)
					if err != nil || (rec.Code != 200 && rec.Code != 404) {
						t.Fatalf("%s %+v: %d %v\n%s", slug, c.req, rec.Code, err, e.logs.String())
					}
					mustContain(t, rec.Body.String(), `data-theme="`+slug+`"`, "/themes/default/assets/tokens.css", "/themes/"+slug+"/assets/theme.css", "/themes/default/assets/style.css", "</html>")
				}
			}
			rec, _ := e.render(kernel.KindRequest{Kind: KindHome}, list())
			for _, v := range settings {
				mustContain(t, rec.Body.String(), v)
			}
		})
	}
}
