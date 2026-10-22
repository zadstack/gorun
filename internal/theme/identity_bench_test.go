package theme

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
)

// BenchmarkIdentityStylesheet measures the CSS build for a fully set
// identity (every key, both scripts, custom CSS).
func BenchmarkIdentityStylesheet(b *testing.B) {
	get := identityGetter(map[string]string{idAccent: "#c2410c", idBackground: "#fffdf8", idText: "#1b1b1f", idHeading: "readex", idBody: "ibm-plex-arabic",
		idLatinFont: "inter", idScheme: "light", idRadius: "soft", idSpacing: "relaxed", idCustomCSS: ".hero{padding:var(--space-6)}"})
	for b.Loop() {
		if IdentityStylesheet(get) == "" {
			b.Fatal("empty")
		}
	}
}

// BenchmarkIdentityPreviewHome renders the identity preview of the home
// page for every catalog theme in both locales, with a draft that sets
// every identity key and a realistic post list.
func BenchmarkIdentityPreviewHome(b *testing.B) {
	for _, slug := range []string{"default", "business", "magazine", "portfolio", "docs"} {
		for _, locale := range []string{"ar", "en"} {
			b.Run(slug+"/"+locale, func(b *testing.B) {
				e := newEnv(b, "../../themes", func(c *config.Config) { c.Theme = slug })
				for i := 0; i < 12; i++ {
					e.fake.Content.Add(content.Item{ID: "p" + string(rune('a'+i)), Type: "post", Title: "Post " + string(rune('a'+i)), Slug: "post-" + string(rune('a'+i)), Status: "published", Locale: locale, Body: "<p>Body text</p>"})
				}
				s := snap(slug, map[string]string{idAccent: "#c2410c", idBackground: "#fffdf8", idText: "#1b1b1f", idHeading: "readex", idBody: "ibm-plex-arabic", idLatinFont: "inter", idRadius: "soft", idSpacing: "relaxed"}, map[string]string{"site.title": "Bench", "site.tagline": "Tagline"})
				ctx := adminCtx()
				b.ResetTimer()
				for b.Loop() {
					r := httptest.NewRequest(http.MethodGet, "/admin/appearance/identity/preview", nil).WithContext(ctx)
					rec := httptest.NewRecorder()
					if err := e.svc.RenderIdentityPreview(ctx, rec, r, IdentityPreviewOptions{View: "home", Locale: locale, Snapshot: s}); err != nil || rec.Code != 200 {
						b.Fatal(err, rec.Code)
					}
				}
			})
		}
	}
}
