package theme

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// The section-library pilot page (testdata/sections-pilot.<locale>.html) is
// what an editor would paste into a page body: it goes through the content
// sanitizer first, so the test proves the styled markup is the markup that
// survives. The same fixture is imported by e2e/tests/sections-pilot.spec.ts.
func pilotFixture(t testing.TB, locale string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sections-pilot."+locale+".html"))
	if err != nil {
		t.Fatal(err)
	}
	return content.Sanitize(string(raw))
}

func pilotPage(locale string) content.Item {
	title := "Section library pilot"
	if locale == "ar" {
		title = "تجربة مكتبة الأقسام"
	}
	return content.Item{Type: "page", Slug: "sections-pilot-" + locale, Title: title, Status: "published"}
}

func TestSanitizerKeepsPilotSections(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		body := pilotFixture(t, locale)
		mustContain(t, body,
			`class="gp-s-hero`, `gp-s-bg`, `gp-s-bleed`, `class="gp-s-facts"`, `class="gp-s-preview"`, `class="gp-s-preview-ui"`,
			`gp-s-icon gp-s-icon-bolt`, `gp-s-card-featured`, `class="gp-s-steps"`, `class="gp-s-flow"`,
			`class="gp-s-compare"`, `class="gp-s-yes"`, `class="gp-s-no"`, `gp-s-faq-cols`, `id="gp-faq"`, `gp-s-cta-panel`, `gp-s-bg-waves`,
			`<details>`, `<summary>`, `<dl`, `<thead>`, `<figcaption>`,
		)
		mustNotContain(t, body, "<svg", "<style", " style=")
	}
}

// Every section class the pilot uses has a rule in sections.css, and the
// stylesheet stays static (no url() to another request, no @import).
func TestSectionsStylesheetCoversPilotAndStaysStatic(t *testing.T) {
	raw, err := os.ReadFile("../../themes/default/assets/sections.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{
		".gp-s-hero-split", ".gp-s-hero-centered", ".gp-s-facts", ".gp-s-media", ".gp-s-icon", ".gp-s-card-featured",
		".gp-s-steps", ".gp-s-flow", ".gp-s-compare", ".gp-s-yes", ".gp-s-no", ".gp-s-faq-cols", ".gp-s-preview", ".gp-s-preview-ui",
		".gp-s-ui-side", ".gp-s-ui-stat", ".gp-s-ui-chart", ".gp-s-ui-rows", ".gp-s-cta-panel",
		".gp-s-bg", ".gp-s-bg-dots", ".gp-s-bg-grid", ".gp-s-bg-lines", ".gp-s-bg-circles", ".gp-s-bg-waves", ".gp-s-bg-paper", ".gp-s-bg-solid", ".gp-s-bleed",
		"--gp-pat-dots-size", "--gp-pat-none", "--gp-pattern-ink", "--gp-pattern-scale",
		"prefers-reduced-motion", `:where(html[dir="rtl"])`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("sections.css lacks %q", want)
		}
	}
	for _, m := range strings.Split(css, "url(")[1:] {
		// url(%23n) is a filter reference inside an embedded SVG, not a request.
		if !strings.HasPrefix(m, `"data:image/svg+xml,`) && !strings.HasPrefix(m, "%23") {
			t.Errorf("sections.css must only embed data: SVG, found url(%s", m[:min(len(m), 40)])
		}
	}
	if strings.Contains(css, "@import") {
		t.Error("sections.css must not @import")
	}
	// Budget stated in the pilot: one static stylesheet shared by every theme,
	// at most 32 KiB raw and 8 KiB gzip (main before the pilot: 5.4 KiB / 1.6 KiB).
	const maxBytes, maxGzip = 32 * 1024, 8 * 1024
	if len(raw) > maxBytes {
		t.Errorf("sections.css is %d bytes, limit %d", len(raw), maxBytes)
	}
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = w.Write(raw)
	_ = w.Close()
	if gz.Len() > maxGzip {
		t.Errorf("sections.css gzips to %d bytes, limit %d", gz.Len(), maxGzip)
	}
}

// renderPilot renders the pilot page in theme slug and locale, failing on
// any status other than 200 or a theme 500 marker.
func renderPilot(t testing.TB, slug, locale string) string {
	t.Helper()
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
	e.setting("site.locale", locale)
	page := pilotPage(locale)
	page.Body = pilotFixture(t, locale)
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: page.Slug}, &Context{Locale: locale, Content: &page}, i18n.WithLocale(t.Context(), locale))
	if err != nil || rec.Code != 200 {
		t.Fatalf("%s/%s: status=%d err=%v\n%s", slug, locale, rec.Code, err, e.logs.String())
	}
	html := rec.Body.String()
	if strings.Contains(html, Marker500) {
		t.Fatalf("%s/%s rendered the 500 page", slug, locale)
	}
	return html
}

func TestSectionsPilotRendersInDefaultAndBusiness(t *testing.T) {
	out := os.Getenv("GORUN_SECTIONS_PILOT_OUT")
	for _, slug := range []string{"default", "business"} {
		for _, locale := range []string{"en", "ar"} {
			t.Run(slug+"/"+locale, func(t *testing.T) {
				html := renderPilot(t, slug, locale)
				mustContain(t, html, "/themes/default/assets/sections.css", `class="gp-s-hero`, `class="gp-s-compare"`, `dir="`+map[string]string{"en": "ltr", "ar": "rtl"}[locale]+`"`)
				// The page template prints the title as an h1 and the hero adds its
				// own (as the starter About page does today); the pilot does not
				// change that. Dedicated page templates are listed as remaining work.
				if !strings.Contains(html, `class="gp-s-hero`) || strings.Count(html, "<h1") < 1 {
					t.Errorf("%s/%s: hero h1 missing", slug, locale)
				}
				if out != "" {
					if err := os.WriteFile(filepath.Join(out, slug+"-"+locale+".html"), []byte(html), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkSectionsPilotRender is the before/after render-time number for the
// pilot (same fixture, same themes): go test ./internal/theme -run xxx -bench SectionsPilot -benchmem
func BenchmarkSectionsPilotRender(b *testing.B) {
	for _, slug := range []string{"default", "business"} {
		for _, locale := range []string{"en", "ar"} {
			b.Run(slug+"/"+locale, func(b *testing.B) {
				e := newEnv(b, "../../themes", func(c *config.Config) { c.Theme = slug })
				e.setting("site.locale", locale)
				page := pilotPage(locale)
				page.Body = pilotFixture(b, locale)
				ctx := i18n.WithLocale(b.Context(), locale)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					rec, err := e.render(kernel.KindRequest{Kind: KindPage, Type: "page", Slug: page.Slug}, &Context{Locale: locale, Content: &page}, ctx)
					if err != nil || rec.Code != 200 {
						b.Fatalf("status=%d err=%v", rec.Code, err)
					}
				}
			})
		}
	}
}
