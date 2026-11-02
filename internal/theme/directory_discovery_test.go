package theme

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDirectoryPagesFilterPublicLocaleAndPreserveOtherState(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`, "components/card.html": `<article>{{.Title}}</article>`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	typ, err := cs.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	typ.Fields = append(typ.Fields, content.Field{Key: "audience", Type: content.FieldSelect, Indexed: true, Label: map[string]string{"en": "Audience"}, Options: []content.Option{{Value: "team", Label: map[string]string{"en": "Teams"}}, {Value: "solo", Label: map[string]string{"en": "Solo"}}}})
	if err := cs.SaveType(ctx, *typ); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 26; i++ {
		_, err := cs.Create(ctx, content.Input{Type: "post", Title: fmt.Sprintf("Team guide %02d", i), Status: "published", Locale: "en", Fields: map[string]any{"audience": "team"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []content.Input{{Type: "post", Title: "Solo guide", Status: "published", Locale: "en", Fields: map[string]any{"audience": "solo"}}, {Type: "post", Title: "Arabic secret", Status: "published", Locale: "ar", Fields: map[string]any{"audience": "team"}}, {Type: "post", Title: "Draft secret", Status: "draft", Locale: "en", Fields: map[string]any{"audience": "team"}}} {
		if _, err := cs.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Directory", Status: "published", Body: `<section id="gp-guides"><div class="gp-s-directory gp-s-dir-post"></div></section>`})
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
	first := render("/directory?audience=team&campaign=keep")
	if strings.Count(first, "<article>") != 24 || !strings.Contains(first, "page=2") || !strings.Contains(first, "campaign=keep") || !strings.Contains(first, "Teams") {
		t.Fatalf("missing paged filtered discovery: %s", first)
	}
	second := render("/directory?audience=team&page=2&campaign=keep")
	if strings.Count(second, "<article>") != 2 {
		t.Fatalf("page2 did not contain remaining two records: %s", second)
	}
	mustNotContain(t, first+second, "Solo guide", "Arabic secret", "Draft secret")
	searched := render("/directory?q=Team+guide+00&audience=team")
	if strings.Count(searched, "<article>") != 1 || !strings.Contains(searched, `class="gp-filter-count">1</span>`) {
		t.Fatalf("facet counts ignore search: %s", searched)
	}
	beyond := render("/directory?audience=team&page=999")
	if !strings.Contains(beyond, "This page has no results") || !strings.Contains(beyond, "First page") {
		t.Fatalf("out of range page lacks recovery: %s", beyond)
	}
}

func TestMultipleDirectoriesHaveIndependentStableQueryState(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`, "components/card.html": `<article>{{.Title}}</article>`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	for _, title := range []string{"Solar guide", "Garden guide"} {
		if _, err := cs.Create(ctx, content.Input{Type: "post", Title: title, Status: "published", Locale: "en"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Two collections", Status: "published", Body: `<section id="gp-solar"><div class="gp-s-directory gp-s-dir-post"></div></section><section id="gp-garden"><div class="gp-s-directory gp-s-dir-post"></div></section>`})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page, Locale: "en", Request: httptest.NewRequest("GET", "/collections?q=solar", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(legacy.Body.String(), "Solar guide") != 2 || strings.Contains(legacy.Body.String(), "Garden guide") {
		t.Fatal("legacy shared search no longer works")
	}
	cleared, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page, Locale: "en", Request: httptest.NewRequest("GET", "/collections?q=solar&dir_gp-solar_q=&dir_gp-garden_q=garden", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(cleared.Body.String(), "Solar guide") != 1 || strings.Count(cleared.Body.String(), "Garden guide") != 2 {
		t.Fatalf("scoped reset reapplied global query or changed sibling: %s", cleared.Body.String())
	}
	for _, layout := range []string{page.Body, `<section id="gp-garden"><div class="gp-s-directory gp-s-dir-post"></div></section><section id="gp-solar"><div class="gp-s-directory gp-s-dir-post"></div></section>`} {
		page.Body = layout
		rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page, Locale: "en", Request: httptest.NewRequest("GET", "/collections?dir_gp-solar_q=solar&dir_gp-garden_q=garden&campaign=%22%3E%3Cscript%3E", nil)})
		if err != nil {
			t.Fatal(err)
		}
		body := rec.Body.String()
		if strings.Count(body, "Solar guide") != 1 || strings.Count(body, "Garden guide") != 1 || !strings.Contains(body, `name="dir_gp-solar_q"`) || !strings.Contains(body, `name="dir_gp-garden_q"`) {
			t.Fatalf("directories share or lose query state: %s", body)
		}
		mustNotContain(t, body, `value=""><script>`, `<script>`)
		if !strings.Contains(body, `name="campaign"`) || !strings.Contains(body, `name="dir_gp-garden_q" value="garden"`) {
			t.Fatalf("GET form lost unrelated state: %s", body)
		}
	}
}

func TestDirectoryHidesPrivateTypesButReportsReadFailure(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`, "components/card.html": `<article>{{.Title}}</article>`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if err := cs.SaveType(ctx, content.Type{Slug: "invoice", Public: false, Name: map[string]string{"en": "Private invoices"}}); err != nil {
		t.Fatal(err)
	}
	render := func(body string) string {
		t.Helper()
		rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: &content.Item{Title: "Directory", Body: body}, Locale: "en", Request: httptest.NewRequest("GET", "/directory", nil)})
		if err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}
	hidden := render(`<div class="gp-s-directory gp-s-dir-invoice"></div>`)
	if strings.Contains(hidden, `role="search"`) || strings.Contains(hidden, `role="alert"`) {
		t.Fatalf("private type exposed: %s", hidden)
	}
	if _, err := e.app.Deps.DB.ExecContext(ctx, `DROP TABLE content`); err != nil {
		t.Fatal(err)
	}
	failed := render(`<div class="gp-s-directory gp-s-dir-post"></div>`)
	if !strings.Contains(failed, `role="alert"`) || !strings.Contains(failed, "could not be loaded") {
		t.Fatalf("query failure masqueraded as empty: %s", failed)
	}
	mustNotContain(t, failed, "Nothing is published", "no such table", "SELECT")
}
