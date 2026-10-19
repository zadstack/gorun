package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
)

const emDash = '\u2014'

// Brief §5: theme-controlled copy has no em dashes and no lorem.
func TestCatalogThemeLangHasNoEmDashOrLorem(t *testing.T) {
	root := filepath.Join("..", "..", "themes")
	for _, slug := range []string{"default", "business", "magazine", "portfolio", "docs"} {
		for _, loc := range []string{"en", "ar"} {
			path := filepath.Join(root, slug, "lang", loc+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if strings.Contains(string(raw), "lorem") || strings.Contains(string(raw), "Lorem") || strings.Contains(string(raw), "ipsum") {
				t.Fatalf("%s contains lorem", path)
			}
			var tree any
			if err := json.Unmarshal(raw, &tree); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			var walk func(any, string)
			walk = func(v any, p string) {
				switch x := v.(type) {
				case map[string]any:
					for k, child := range x {
						walk(child, p+"."+k)
					}
				case []any:
					for _, child := range x {
						walk(child, p+"[]")
					}
				case string:
					if strings.ContainsRune(x, emDash) {
						t.Errorf("%s%s contains em dash: %q", path, p, x)
					}
				}
			}
			walk(tree, "")
		}
	}
}

// Brief §5: shipped themes never put robots:noindex on the home; OG title/description present.
func TestCatalogHomeHasOGAndIsIndexable(t *testing.T) {
	for _, slug := range []string{"default", "business", "magazine", "portfolio", "docs"} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			if err := seo.New().Register(e.app.Deps); err != nil {
				t.Fatal(err)
			}
			e.setting(SettingTitle, "GoRun Demo")
			e.setting(SettingTagline, "A sensible demo site")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			if strings.Contains(body, `name="robots" content="noindex"`) {
				t.Fatal("home must not be noindex")
			}
			mustContain(t, body,
				`property="og:title"`,
				`property="og:description"`,
				`property="og:type" content="website"`,
			)
			// OG image only when a file exists: empty default is OK (tag omitted).
			if strings.Contains(body, `property="og:image" content=""`) {
				t.Fatal("empty og:image must be omitted")
			}
		})
	}
}
