package theme

import (
	"context"
	"html"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDirectoryDetailReturnPreservesDiscoveryWithoutRewritingOtherLinks(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Default","supports":["public-ui"]}`, map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`, "components/card.html": `<article><a class="record" href="{{url .}}">{{.Title}}</a><a class="category" href="/category/guide">Category</a><a class="external" href="https://elsewhere.test/target">External</a></article>`})
	e := bootRealTheme(t, root)
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	for _, in := range []content.Input{{Type: "page", Title: "Target", Slug: "target", Status: "published", Locale: "ar", Template: "public-detail"}, {Type: "page", Title: "Legacy Target", Slug: "legacy", Status: "published", Locale: "ar"}} {
		if _, err := svc.Create(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := svc.Create(context.Background(), content.Input{Type: "page", Title: "Directory", Slug: "directory", Status: "published", Locale: "ar", Body: `<section id="gp-results"><div class="gp-s-directory gp-s-dir-page"></div></section>`})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: directory, Locale: "ar", Request: httptest.NewRequest("GET", "/directory?q=Target&campaign=keep&back=%2Fold%3Fback%3Dnested", nil)})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	match := regexp.MustCompile(`class="record" href="([^"]*target[^\"]*)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal(body)
	}
	destination, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	back, err := url.Parse(destination.Query().Get("back"))
	if err != nil {
		t.Fatal(err)
	}
	if back.Path != "/directory" || back.Query().Get("q") != "Target" || back.Query().Get("campaign") != "keep" || back.Query().Has("back") || back.Fragment != "gp-results" {
		t.Fatalf("bad return: %s", back)
	}
	mustContain(t, body, `href="/category/guide"`, `href="https://elsewhere.test/target"`, `href="http://example.test/legacy"`)
}
