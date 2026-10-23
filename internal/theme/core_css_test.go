package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/pkg/themekit"
)

// Every shipped theme gets the core stylesheet through {{sections .}}, and the
// store stylesheet on commerce pages (resolved to default's file for children
// that never linked it themselves). Before, only default linked commerce.css,
// so store and account pages were unstyled under three themes.
func TestEveryShippedThemeLinksTheCoreStylesheets(t *testing.T) {
	commerceLink := regexp.MustCompile(`<link rel="stylesheet" href="/themes/default/assets/commerce\.css\?v=\d+">`)
	for _, slug := range []string{"default", "business", "portfolio", "magazine", "docs"} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			for _, commerce := range []bool{false, true} {
				rec, err := e.render(kernel.KindRequest{Kind: KindHome}, &Context{Extra: map[string]any{"commerce": commerce}})
				if err != nil {
					t.Fatal(err)
				}
				body := rec.Body.String()
				head := body[:strings.Index(body, "</head>")]
				if n := strings.Count(head, CoreCSSURL()); n != 1 {
					t.Fatalf("core stylesheet links in <head> = %d, want 1", n)
				}
				want := 0
				if commerce {
					want = 1
				}
				if n := len(commerceLink.FindAllString(head, -1)); n != want {
					t.Fatalf("commerce=%v: store stylesheet links = %d, want %d", commerce, n, want)
				}
			}
		})
	}
}

// The shared functional templates wrap their content in .gp-core, the scope
// the core utilities and element defaults are written for.
func TestSharedFunctionalTemplatesUseTheCoreScope(t *testing.T) {
	for _, name := range []string{"account", "cart", "checkout", "checkout-result", "shop", "product", "private-intake", "form-result"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "default", "templates", name+".html"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if !strings.Contains(s, `{{part "header" .}}<div class="gp-core">`) || !strings.Contains(s, `</div>{{part "footer" .}}`) {
			t.Errorf("%s.html must wrap its content in <div class=\"gp-core\">", name)
		}
	}
}

// The core stylesheet carries no reset (it would flatten each theme's
// typography), scopes its utilities under .gp-core, and keeps element defaults
// at zero specificity outside that scope.
func TestCoreStylesheetIsScopedAndResetFree(t *testing.T) {
	css := string(CoreCSS())
	if css == "" {
		t.Fatal("web/site/core.css is not embedded")
	}
	for _, want := range []string{".gp-core .mx-auto{", ".gp-core .sr-only{", ":where(main) :where(label)", ".gp-core :where(label)"} {
		if !strings.Contains(css, want) {
			t.Errorf("core.css lacks %q", want)
		}
	}
	for _, reset := range []string{"h1,h2,h3,h4,h5,h6{font-size:inherit", "menu,ol,ul{list-style:none", "button,input,optgroup,select,textarea{"} {
		if strings.Contains(css, reset) {
			t.Errorf("core.css contains the Tailwind reset rule %q", reset)
		}
	}
	// A bare utility (".mx-auto{") would restyle a theme's own markup.
	if regexp.MustCompile(`(^|[},])\.(mx-auto|flex|grid|hidden|block|table)\{`).MatchString(css) {
		t.Error("core.css has an unscoped utility")
	}
	if CoreCSSPath != themekit.CoreStylesheetPath {
		t.Errorf("theme.CoreCSSPath %q and themekit.CoreStylesheetPath %q differ", CoreCSSPath, themekit.CoreStylesheetPath)
	}
	if !regexp.MustCompile(`^/static/site/core\.css\?v=[0-9a-f]{12}$`).MatchString(CoreCSSURL()) {
		t.Errorf("CoreCSSURL() = %q", CoreCSSURL())
	}
}
