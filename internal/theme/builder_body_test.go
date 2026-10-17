package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"strings"
	"testing"
)

func TestBuilderDynamicContentRendersAtItsSectionPosition(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{
		"templates/page-builder.html":  `{{builderBody .Content}}`,
		"components/contact-form.html": `<form action="/forms/contact"><label for="cf-name">Name</label><input id="cf-name"></form>`,
		"components/card.html":         `<article>{{.Title}}</article>`,
	})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	_, err := cs.Create(ctx, content.Input{Type: "post", Title: "Published live story", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cs.Create(ctx, content.Input{Type: "post", Title: "SECRET DRAFT STORY", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Builder", Status: "published", Template: "builder", Body: `<div class="gp-builder"><section class="gp-section gp-contact gp-default"><div class="gp-inner"><h1>Contact first</h1></div></section><section class="gp-section gp-text gp-default gp-feed-posts"><div class="gp-inner"><h2>Stories second</h2></div></section><section class="gp-section gp-text gp-default"><h2>Last section</h2></section></div>`})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: it})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	positions := []int{strings.Index(body, "Contact first"), strings.Index(body, "<form"), strings.Index(body, "Stories second"), strings.Index(body, "Published live story"), strings.Index(body, "Last section")}
	for i, pos := range positions {
		if pos < 0 || (i > 0 && pos <= positions[i-1]) {
			t.Fatalf("dynamic section order broken %v: %s", positions, body)
		}
	}
	mustNotContain(t, body, "SECRET DRAFT STORY")
}

func TestBuilderHeroLoadsTrustedModuleOnlyWhenPresent(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	for _, scene := range []string{"", "orbit", "ribbons", "blocks"} {
		markup := `<section class="gp-section gp-hero"><h1>Hero</h1>`
		if scene != "" {
			markup += `<div class="gp-hero-scene gp-scene-` + scene + `"><div class="gp-scene-fallback"><span></span><span></span><span></span></div></div>`
		}
		markup += `<script src="https://evil.test/a.js"></script></section>`
		it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Hero " + scene, Status: "published", Template: "builder", Body: markup})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: it})
		if err != nil {
			t.Fatal(err)
		}
		body := rec.Body.String()
		if strings.Contains(body, "hero-scenes.js") != (scene != "") {
			t.Fatalf("conditional asset failed: %s", body)
		}
		mustNotContain(t, body, "evil.test")
		if scene != "" && !strings.Contains(body, "<span></span>") {
			t.Fatalf("fallback lost: %s", body)
		}
	}
}
