package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/pkg/themekit"
)

// TestSharedBaseTokensLoadFirst checks every catalog theme head links the
// shared tokens stylesheet before its personality CSS.
func TestSharedBaseTokensLoadFirst(t *testing.T) {
	base, err := os.ReadFile("../../themes/_base/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	served, err := os.ReadFile("../../themes/default/assets/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	servedBody := string(served)
	if i := strings.Index(servedBody, "/* GoRun shared theme tokens"); i >= 0 {
		servedBody = servedBody[i:]
	}
	if string(base) != servedBody {
		t.Fatal("themes/default/assets/tokens.css must match themes/_base/tokens.css (served copy)")
	}
	for _, tok := range []string{"--bg:", "--accent:", "--font:", "--site-width:", "html[dir=\"rtl\"] time"} {
		if !strings.Contains(string(base), tok) {
			t.Fatalf("base tokens missing %q", tok)
		}
	}

	e := newEnv(t, "../../themes")
	for _, slug := range themekit.CatalogSlugs {
		t.Run(slug, func(t *testing.T) {
			if err := e.svc.Activate(t.Context(), slug); err != nil {
				t.Fatal(err)
			}
			rec, err := e.render(kernel.KindRequest{Kind: Kind404}, &Context{Status: 404})
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			mustContain(t, body, "/themes/default/assets/tokens.css")
			ti := strings.Index(body, "tokens.css")
			if ti < 0 {
				t.Fatal("tokens.css link missing")
			}
			// Personality CSS should not precede the shared tokens file.
			for _, marker := range []string{"site.css", "theme.css", "style.css"} {
				if mi := strings.Index(body, marker); mi >= 0 && mi < ti {
					t.Fatalf("%s linked before tokens.css", marker)
				}
			}
		})
	}
}

// TestCatalogThemesOmitTailwindRuntime ensures shipped themes never load the
// in-browser Tailwind Play build (CDN or local vendor runtime).
func TestCatalogThemesOmitTailwindRuntime(t *testing.T) {
	root := filepath.Clean("../../themes")
	for _, slug := range themekit.CatalogSlugs {
		t.Run(slug, func(t *testing.T) {
			_ = filepath.WalkDir(filepath.Join(root, slug), func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				if filepath.Base(path) == "tailwind-runtime.html" {
					return nil // opt-in part may exist; must not be included from head*
				}
				ext := strings.ToLower(filepath.Ext(path))
				if ext != ".html" {
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				body := string(raw)
				rel, _ := filepath.Rel(root, path)
				if strings.Contains(body, "cdn.tailwindcss.com") || strings.Contains(body, "tailwindcss-3.4.17.js") {
					t.Errorf("%s references Tailwind runtime", rel)
				}
				if strings.Contains(body, `part "tailwind-runtime"`) && !strings.Contains(rel, "tailwind-runtime") {
					t.Errorf("%s includes tailwind-runtime part", rel)
				}
				return nil
			})
		})
	}
}
