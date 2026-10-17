package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
	"strings"
	"testing"
)

func TestOverviewAssistantOnlyRealContent(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page.html": "<h1>{{.Content.Title}}</h1>"})
	e := bootRealTheme(t, root)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it, err := cs.Create(context.Background(), content.Input{Type: "page", Title: "Real editable page", Body: "Source", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	e.app.Deps.Services.Set(kernel.ServiceSEO, seo.Service(sitemapSEO{entries: []seo.SitemapEntry{{Loc: "https://example.com/"}, {Loc: "https://example.com/page", Item: it}, {Loc: "https://example.com/blog", Type: &content.Type{Slug: "post"}}}}))
	c := e.client()
	e.login(c, "assistant-owner@example.com", "admin")
	resp, body := c.get("/admin/appearance/overview")
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if strings.Count(body, `data-page-assistant="`) != 1 || !strings.Contains(body, `data-page-assistant="`+it.ID+`"`) {
		t.Fatal("assistant must bind only real editable content, never synthetic routes")
	}
	mustContain(t, body, `id="page-assistant-dialog"`, `page-assistant.js`, `page-assistant.css`)
}
