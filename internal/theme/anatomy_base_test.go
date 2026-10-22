package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// TestAnatomyCSSShipped ensures the shared anatomy stylesheet exists and is
// linked from every catalog theme home (via parts/shell.html).
func TestAnatomyCSSShipped(t *testing.T) {
	path := filepath.Join("..", "..", "themes", "default", "assets", "anatomy.css")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{".gr-hero", ".gr-section-head", ".gr-card", ".gr-list-row", ".gr-closing", ".gr-facts"} {
		if !strings.Contains(body, want) {
			t.Errorf("anatomy.css missing %q", want)
		}
	}

	for _, slug := range []string{"default", "business", "magazine", "portfolio", "docs"} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			// Themes whose header already has a search control render no
			// fallback button; a configured CTA uses the shared anatomy button.
			e.setting("theme."+slug+".cta_label", "Book a call")
			e.setting("theme."+slug+".cta_url", "/contact")
			post := e.fake.Content.Add(content.Item{
				Type: "post", Slug: "hello", Title: "Hello", Body: "<p>Body</p>",
				Status: "published", PublishedAt: "2026-10-10T00:00:00Z",
			})
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, &Context{
				Items:      []content.Item{post},
				Pagination: NewPagination(1, 1, 1),
			})
			if err != nil || rec.Code != 200 {
				t.Fatalf("status=%d err=%v\n%s", rec.Code, err, e.logs.String())
			}
			html := rec.Body.String()
			mustContain(t, html, "/themes/default/assets/anatomy.css", "gr-btn", "Book a call")
			if slug == "default" {
				mustContain(t, html, "gr-hero", "gr-latest", "gr-closing")
			}
		})
	}
}
