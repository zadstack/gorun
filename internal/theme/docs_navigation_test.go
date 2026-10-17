package theme

import (
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"testing"
)

func TestDocsOffersNativeMobilePrimaryNavigation(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "docs" })
	_, err := e.fake.Menus.Save(t.Context(), menus.Menu{Slug: "main", Location: "primary", Items: []menus.Item{{Label: "Reference", URL: "/reference", OpenInNew: true}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.render(kernel.KindRequest{Kind: KindHome}, &Context{})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, r.Body.String(), `class="docs-mobile-menu"`, `<summary>`, `href="http://example.test/reference"`, `rel="noopener"`)
}
