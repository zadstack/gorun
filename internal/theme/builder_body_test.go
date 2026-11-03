package theme

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
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

const formSectionBody = `<div class="gp-builder"><section class="gp-section gp-form gp-default" data-form="booking"><div class="gp-inner"><div class="gp-heading"><h2>Book</h2></div></div></section></div>`

// fakeForm registers a "form" func like site's and records its arguments.
func fakeForm(e *env, got *[]string) {
	e.app.Deps.Theme.AddFunc("form", func(slug, csrf, back string, locale ...string) template.HTML {
		*got = append([]string{slug, csrf, back}, locale...)
		return template.HTML(`<form class="sb-form" method="post" action="/forms/` + slug + `"><input type="hidden" name="_csrf" value="` + csrf +
			`"><label for="sf-booking-name">Name</label><input id="sf-booking-name" name="name" required><select name="room"><option>A</option></select><textarea name="note"></textarea><button type="submit">Book</button></form>`)
	})
}

func TestBuilderBodyInjectsForm(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`})
	e := bootRealTheme(t, root)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it, err := cs.Create(context.Background(), content.Input{Type: "page", Title: "Book", Status: "published", Template: "builder", Body: formSectionBody})
	if err != nil {
		t.Fatal(err)
	}
	render := func() (string, string) {
		var body, token string
		h := auth.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token = auth.CSRFToken(r.Context())
			rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: it, Locale: "en", Request: r}, r.Context())
			if err != nil {
				t.Fatal(err)
			}
			body = rec.Body.String()
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/book", nil))
		return body, token
	}
	// Without a registered form func the section stays as it is.
	body, _ := render()
	mustContain(t, body, `data-form="booking"`, "Book")
	mustNotContain(t, body, "<form")

	var got []string
	fakeForm(e, &got)
	body, token := render()
	if token == "" || len(got) != 4 || got[0] != "booking" || got[1] != token || got[2] != "/book" || got[3] != "en" {
		t.Fatalf("form func called with %q (request token %q)", got, token)
	}
	mustContain(t, body, `name="_csrf" value="`+token+`"`, `action="/forms/booking"`)
	inner := strings.Index(body, `class="gp-inner"`)
	heading := strings.Index(body, "</h2>")
	form := strings.Index(body, "<form")
	if inner < 0 || form < heading || form < inner || strings.Index(body, "</form>") > strings.LastIndex(body, "</section>") {
		t.Fatalf("form not appended inside .gp-inner: %s", body)
	}
	mustNotContain(t, body, "disabled")
}

func TestBuilderBodyFormEmptyLeavesSection(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`})
	e := bootRealTheme(t, root)
	e.app.Deps.Theme.AddFunc("form", func(slug, csrf, back string, locale ...string) template.HTML { return "" })
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it, err := cs.Create(context.Background(), content.Input{Type: "page", Title: "Book", Status: "published", Template: "builder", Body: formSectionBody})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: it})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `data-form="booking"`)
	mustNotContain(t, rec.Body.String(), "<form")
}

func TestPreviewFormDisabled(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `<!doctype html><html><head></head><body>{{builderBody .Content}}</body></html>`})
	e := newEnv(t, root)
	var got []string
	fakeForm(e, &got)
	item := &content.Item{ID: "preview", Type: "page", Slug: "book", Title: "Book", Body: formSectionBody, Locale: "ar"}
	page, err := e.svc.RenderBuilderThemePreview(t.Context(), item, BuilderPreviewOptions{Theme: "default", Identity: brand.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[3] != "ar" {
		t.Fatalf("form func args %q", got)
	}
	for _, tag := range regexp.MustCompile(`<(input|select|textarea|button)\b[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(tag, "disabled") {
			t.Fatalf("preview control not disabled: %s\n%s", tag, page)
		}
	}
	mustContain(t, page, `<button type="submit" disabled="">`, `name="name" required="" disabled=""`)
}
