package theme

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestStudioPortfolioPublicShell(t *testing.T) {
	for _, locale := range []string{"ar", "en"} {
		t.Run(locale, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "portfolio" })
			e.setting("site.locale", locale)
			page := content.Item{Type: "page", Slug: "work", Title: "Selected work", Template: "builder", Body: `<div class="gp-builder"><section class="gp-section gp-projects"><div class="gp-inner"><div class="gp-heading"><h1>Selected work</h1></div><div class="gp-grid"><article class="gp-entry"><img class="gp-image" src="/themes/portfolio/assets/images/project-orbit.svg" alt="Abstract composition"><h3>Orbit</h3></article></div></div></section></div>`}
			for _, kind := range []string{KindPage, KindSingle, KindHome, KindArchive, KindSearch, Kind404} {
				tc := &Context{Locale: locale, Content: &page, Items: []content.Item{page}, Query: map[string]string{"q": "Orbit"}}
				req := kernel.KindRequest{Kind: kind, Type: "page", Slug: "work"}
				if kind == KindPage {
					req.Template = "builder"
				}
				rec, err := e.render(req, tc, i18n.WithLocale(t.Context(), locale))
				if err != nil {
					t.Fatalf("%s: %v", kind, err)
				}
				mustContain(t, rec.Body.String(), `data-theme="portfolio"`, `/themes/portfolio/assets/site.css`, `<details class="studio-navigation"`, `id="main"`, `</html>`, `lang="`+locale+`"`)
				if strings.Contains(rec.Body.String(), "https://cdn.") || strings.Contains(rec.Body.String(), "fonts.googleapis") {
					t.Fatal("theme depends on remote resources")
				}
				if kind == KindPage {
					mustContain(t, rec.Body.String(), "Selected work", "project-orbit.svg")
				}
			}
		})
	}
}

func TestStudioPortfolioLocalArtworkAndGeometry(t *testing.T) {
	for _, name := range []string{"project-orbit.svg", "project-grid.svg", "project-form.svg", "studio-mark.svg"} {
		data, err := os.ReadFile("../../themes/portfolio/assets/images/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var image struct {
			XMLName xml.Name
			ViewBox string `xml:"viewBox,attr"`
		}
		if err := xml.Unmarshal(data, &image); err != nil {
			t.Fatal(err)
		}
		if image.XMLName.Local != "svg" || image.ViewBox == "" {
			t.Fatalf("invalid artwork: %s", name)
		}
		if strings.Contains(string(data), "http://example") || strings.Contains(string(data), "<script") {
			t.Fatalf("unsafe artwork: %s", name)
		}
	}
	css, err := os.ReadFile("../../themes/portfolio/assets/site.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"hero", "text", "features", "projects", "testimonials", "pricing", "stats", "faq", "services", "logos", "cta", "contact"} {
		if !strings.Contains(string(css), ".gp-"+kind) {
			t.Fatalf("missing section geometry: %s", kind)
		}
	}
	for _, required := range []string{"var(--gp-width", "var(--gp-bg", "var(--gp-text", "var(--gp-accent", "@media(max-width:700px)", ".hidden"} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("missing identity/mobile/form styling: %s", required)
		}
	}
}

func TestStudioPortfolioContactSlugPreservesBuilderSections(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "portfolio" })
	page := content.Item{Type: "page", Slug: "contact", Title: "Contact", Template: "builder", Body: `<div class="gp-builder"><section class="gp-section gp-contact"><div class="gp-inner"><h1>Contact first</h1></div></section><section id="gp-after" class="gp-section gp-text"><div class="gp-inner"><h2>After form</h2></div></section></div>`}
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: "contact", Template: "builder"}, &Context{Content: &page})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Count(body, `id="cf-name"`) != 1 || strings.Index(body, `id="cf-name"`) > strings.Index(body, `id="gp-after"`) {
		t.Fatal("builder contact form must remain in section before following content")
	}
}

func TestStudioPortfolioErrorPageIsSelfContained(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "portfolio" })
	rec, err := e.render(kernel.KindRequest{Kind: Kind500}, &Context{})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `name="robots" content="noindex"`)
	mustNotContain(t, rec.Body.String(), "studio-header", "studio-navigation", "server_error_title")
}

func TestStudioPortfolioTaxonomyAndFormStates(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "portfolio" })
	rec, err := e.render(kernel.KindRequest{Kind: KindTaxonomy, Taxonomy: "category", Term: "design"}, &Context{Term: &taxonomy.Term{Name: "Design archive", Description: "Portfolio notes"}})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), "studio-taxonomy-title", "Design archive", "Portfolio notes", "studio-footer")
	e.app.Deps.Theme.AddKind("form-result", func(kernel.KindRequest) []string { return []string{"form-result.html"} })
	type result struct {
		OK     bool
		Errors map[string]string
		Values map[string]string
		Back   string
	}
	for _, success := range []bool{true, false} {
		data := result{OK: success, Errors: map[string]string{"email": "contact_email"}, Values: map[string]string{"name": "Saved name"}, Back: "/contact"}
		rec, err = e.render(kernel.KindRequest{Kind: "form-result"}, &Context{Extra: map[string]any{"form": data}})
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, rec.Body.String(), "studio-form-result", "studio-footer", `href="/contact"`)
		if success {
			mustContain(t, rec.Body.String(), `role="status"`)
			mustNotContain(t, rec.Body.String(), `id="cf-name"`)
		} else {
			mustContain(t, rec.Body.String(), `role="alert"`, `id="cf-name"`, "Saved name")
		}
	}
}
