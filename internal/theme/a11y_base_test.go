package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/pkg/themekit"
)

// TestSharedA11yCSSShipped ensures the shared a11y stylesheet matches the base
// copy and is linked from every catalog theme after tokens.css.
func TestSharedA11yCSSShipped(t *testing.T) {
	base, err := os.ReadFile("../../themes/_base/a11y.css")
	if err != nil {
		t.Fatal(err)
	}
	served, err := os.ReadFile("../../themes/default/assets/a11y.css")
	if err != nil {
		t.Fatal(err)
	}
	if string(base) != string(served) {
		t.Fatal("themes/default/assets/a11y.css must match themes/_base/a11y.css")
	}
	for _, want := range []string{
		":focus-visible",
		"prefers-reduced-motion",
		"--tap-min",
		".gr-skip",
		`html[dir="rtl"]`,
		"letter-spacing: 0",
	} {
		if !strings.Contains(string(base), want) {
			t.Fatalf("a11y.css missing %q", want)
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
			mustContain(t, body, "/themes/default/assets/a11y.css")
			ti := strings.Index(body, "tokens.css")
			ai := strings.Index(body, "a11y.css")
			if ti < 0 || ai < 0 || ai < ti {
				t.Fatalf("a11y.css must link after tokens.css (tokens=%d a11y=%d)", ti, ai)
			}
			mustContain(t, body, `href="#main"`)
		})
	}
}

// TestMutedTokenMeetsAAOnPanel2 guards the shared --muted / --panel-2 pair.
func TestMutedTokenMeetsAAOnPanel2(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "_base", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	muted := cssVarHex(body, "--muted")
	panel2 := cssVarHex(body, "--panel-2")
	bg := cssVarHex(body, "--bg")
	panel := cssVarHex(body, "--panel")
	if muted == "" || panel2 == "" {
		t.Fatal("missing --muted or --panel-2 in tokens")
	}
	for _, pair := range []struct{ fg, bg, name string }{
		{muted, panel2, "muted/panel-2"},
		{muted, bg, "muted/bg"},
		{muted, panel, "muted/panel"},
	} {
		if r := contrast(pair.fg, pair.bg); r < 4.5 {
			t.Errorf("%s contrast %.2f < 4.5 (%s on %s)", pair.name, r, pair.fg, pair.bg)
		}
	}
}

func cssVarHex(css, name string) string {
	i := strings.Index(css, name+":")
	if i < 0 {
		return ""
	}
	rest := css[i+len(name)+1:]
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "#") {
		return ""
	}
	end := strings.IndexAny(rest, ";\n ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
