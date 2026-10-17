package theme

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestStudioBusinessRendersCompleteNativeTemplates(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	e := bootRealTheme(t, root)
	if err := e.svc.Activate(context.Background(), "business"); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"ar", "en"} {
		for _, tc := range []struct {
			kind, template string
			item           *content.Item
			want           string
		}{
			{KindHome, "", nil, "sb-home"},
			{KindPage, "", &content.Item{Title: "Company page", Body: "<p>Readable copy</p>"}, "sb-document"},
			{KindPage, "builder", &content.Item{Title: "Built page", Body: `<div class="gp-builder"><section class="gp-section gp-hero"><h1>Built hero</h1></section></div>`}, "Built hero"},
			{KindSingle, "", &content.Item{Title: "Journal entry", Body: "<p>Article content</p>"}, "sb-article"},
			{KindArchive, "", nil, "sb-list"},
			{KindSearch, "", nil, "search-q"},
			{Kind404, "", nil, "404"},
			{KindPage, "contact", &content.Item{Title: "Contact"}, "/forms/contact"},
		} {
			t.Run(locale+tc.kind+tc.template, func(t *testing.T) {
				rec, err := e.render(kernel.KindRequest{Kind: tc.kind, Template: tc.template}, &Context{Content: tc.item, Locale: locale})
				if err != nil {
					t.Fatal(err)
				}
				body := rec.Body.String()
				mustContain(t, body, `data-theme="business"`, "site.css", `id="main"`, "sb-footer", tc.want)
				mustContain(t, body, `lang="`+locale+`"`)
				if tc.template == "contact" {
					mustContain(t, body, `name="_csrf"`, `name="_back"`, `name="website"`, `name="email"`, `name="message"`, `class="sb-sr" aria-hidden="true"`)
				}
				mustNotContain(t, body, "<script>alert(", "cdn.", "fonts.googleapis.com", "href=\"#\"")
			})
		}
	}
	css, err := os.ReadFile(filepath.Join(root, "business/assets/site.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"hero", "text", "features", "services", "projects", "testimonials", "pricing", "faq", "stats", "logos", "contact", "cta"} {
		mustContain(t, string(css), ".gp-"+kind)
	}
}

func TestStudioBusinessLocalAssetsAndRenderedIdentity(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	e := bootRealTheme(t, root)
	if err := e.svc.Activate(t.Context(), "business"); err != nil {
		t.Fatal(err)
	}
	e.setting("builder.identity", map[string]string{"accent": "#167252", "background": "#101010", "text": "#eeeeee", "font": "mono", "width": "wide"})
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), "--gp-width:1280px", "--bg:#101010", "ui-monospace")
	for _, asset := range []string{"site.css", "images/strategy.svg", "images/identity.svg", "images/delivery.svg"} {
		body, err := os.ReadFile(filepath.Join(root, "business", "assets", filepath.FromSlash(asset)))
		if err != nil || len(body) == 0 {
			t.Fatalf("local asset %s: %v", asset, err)
		}
		if strings.HasSuffix(asset, ".svg") {
			var artwork struct{ XMLName xml.Name }
			if err := xml.Unmarshal(body, &artwork); err != nil {
				t.Fatalf("invalid SVG %s: %v", asset, err)
			}
			if artwork.XMLName.Local != "svg" || artwork.XMLName.Space != "http://www.w3.org/2000/svg" {
				t.Fatalf("asset %s is not SVG: %+v", asset, artwork.XMLName)
			}
		}
	}
}

func TestStudioBusinessBuilderContactRetainsSectionPosition(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "business" })
	page := e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact", Template: "builder", Status: "published", Body: `<section class="gp-section gp-contact"><div class="gp-inner"><h1>Write to us</h1></div></section><section class="gp-section gp-text"><div class="gp-inner"><h2>After the contact form</h2><p>Editorial guidelines</p></div></section>`})
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact", Template: "builder"}, &Context{Content: &page})
	if err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, e.logs.String())
	}
	body := rec.Body.String()
	if got := strings.Count(body, `action="/forms/contact"`); got != 1 {
		t.Fatalf("forms=%d, want exactly one", got)
	}
	formAt := strings.Index(body, `action="/forms/contact"`)
	headingAt := strings.Index(body, ">After the contact form</h2>")
	if formAt < 0 || headingAt < 0 || formAt > headingAt {
		t.Fatalf("contact form appeared after following section: form=%d next=%d", formAt, headingAt)
	}
	mustContain(t, body, `class="sb-built-page"`)
	mustNotContain(t, body, `class="sb-contact-page"`)
}
