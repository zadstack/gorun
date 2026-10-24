package theme

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// A directory section lists the published items of its type and narrows them
// by the request's q; drafts, private types and unsafe input never leak.
func TestDirectorySectionSearchesPublishedItems(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{
		"templates/page-builder.html": `{{builderBody .Content}}`,
		"components/card.html":        `<article>{{.Title}}</article>`,
	})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	for _, in := range []content.Input{
		{Type: "post", Title: "Solar panels guide", Status: "published", Locale: "en"},
		{Type: "post", Title: "Garden tools guide", Status: "published", Locale: "en"},
		{Type: "post", Title: "Solar draft secret", Status: "draft", Locale: "en"},
	} {
		if _, err := cs.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := cs.Create(ctx, content.Input{Type: "page", Slug: "directory", Title: "Directory", Status: "published", Template: "builder",
		Body: `<section id="gp-dir" class="gp-s-band"><h2>Guides</h2><div class="gp-s-directory gp-s-dir-post"></div></section><section class="gp-s-band"><div class="gp-s-directory gp-s-dir-nosuchtype"></div></section>`})
	if err != nil {
		t.Fatal(err)
	}
	render := func(target string) string {
		t.Helper()
		rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page, Locale: "en", Request: httptest.NewRequest("GET", target, nil)})
		if err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}

	all := render("/directory")
	for _, want := range []string{`<form class="gp-core gp-s-dir-search" role="search" method="get" action="/directory">`, "Solar panels guide", "Garden tools guide"} {
		if !strings.Contains(all, want) {
			t.Errorf("full listing lacks %q:\n%s", want, all)
		}
	}
	mustNotContain(t, all, "Solar draft secret")
	if strings.Count(all, `role="search"`) != 1 || !strings.Contains(all, `<div class="gp-s-directory gp-s-dir-nosuchtype"></div>`) {
		t.Errorf("an unknown type should leave its slot empty, not fail the page:\n%s", all)
	}

	hits := render(`/directory?q=solar&x=<b>`)
	if !strings.Contains(hits, "Solar panels guide") || strings.Contains(hits, "Garden tools guide") || !strings.Contains(hits, `value="solar"`) || !strings.Contains(hits, "1 results for") {
		t.Errorf("search did not narrow the list:\n%s", hits)
	}
	mustNotContain(t, hits, "Solar draft secret")

	miss := render(`/directory?q="><script>alert(1)</script>`)
	mustNotContain(t, miss, "<script>alert")
	if !strings.Contains(miss, "Nothing matches this search yet.") || !strings.Contains(miss, `<a href="/directory">Show all</a>`) {
		t.Errorf("empty search state missing:\n%s", miss)
	}
}
