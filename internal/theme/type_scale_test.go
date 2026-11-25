package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/pkg/themekit"
)

// The served copy of the shared tokens is the base file, byte for byte.
func TestSharedTokensServedCopyMatches(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "..", "themes", "_base", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	served, err := os.ReadFile(filepath.Join("..", "..", "themes", "default", "assets", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	if string(base) != string(served) {
		t.Fatal("themes/default/assets/tokens.css must match themes/_base/tokens.css")
	}
	// Arabic lines are taller at every step.
	if !strings.Contains(string(base), "html[dir=\"rtl\"] {\n  --leading-tight:") {
		t.Error("tokens.css should raise the line heights under html[dir=\"rtl\"]")
	}
}

// Both Tailwind builds of the public side read the type scale: the default
// theme's utilities and the core stylesheet's utilities and Basecoat
// components write the steps and their line heights, never a fixed rem.
func TestPublicStylesheetsReadTheTypeScale(t *testing.T) {
	core := string(CoreCSS())
	for _, want := range []string{
		"--text-sm: var(--fs-sm);", "--text-sm--line-height: var(--leading-normal);",
		"--text-base: var(--fs-base);", "--text-lg: var(--fs-lg);", "--text-lg--line-height: var(--leading-snug);",
		".gp-core .text-sm{font-size:var(--fs-sm);line-height:var(--leading-normal)}",
		".gp-core :where(h1){font-size:var(--fs-4xl,2.375rem);line-height:var(--leading-tight,1.15)}",
		".gp-core :where(h2){font-size:var(--fs-2xl,1.5rem);",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("core.css lacks %q", want)
		}
	}
	for _, never := range []string{"--text-sm: 0.875rem;", "--text-base: 1rem;", ".gp-core .text-sm{font-size:.875rem"} {
		if strings.Contains(core, never) {
			t.Errorf("core.css still carries Tailwind's fixed size %q", never)
		}
	}
	tw, err := os.ReadFile(filepath.Join("..", "..", "themes", "default", "assets", "tailwind.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".text-sm{font-size:var(--fs-sm)", ".text-2xl{font-size:var(--fs-2xl);line-height:var(--leading-tight)}"} {
		if !strings.Contains(string(tw), want) {
			t.Errorf("default tailwind.css lacks %q", want)
		}
	}
	if strings.Contains(string(tw), ".text-sm{font-size:.875rem") {
		t.Error("default tailwind.css still sizes text-sm in rem")
	}
}

// Every catalog theme writes its type on the shared steps: the stylesheets
// reference var(--fs-*) and keep no fixed heading sizes (a px or rem
// font-size on h1..h3). Decorative sizes (em, %, numerals) are not headings.
func TestCatalogThemesWriteTypeOnTheScale(t *testing.T) {
	fixedHeading := regexp.MustCompile(`\bh[1-3]\b[^{}]*\{[^}]*font-size:\s*(\d|\.\d|clamp\()`)
	for _, slug := range themekit.CatalogSlugs {
		matches, err := filepath.Glob(filepath.Join("..", "..", "themes", slug, "assets", "*.css"))
		if err != nil {
			t.Fatal(err)
		}
		uses := 0
		for _, p := range matches {
			name := filepath.Base(p)
			if name == "tailwind.css" || name == "tokens.css" || name == "a11y.css" {
				continue
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			css := string(raw)
			uses += strings.Count(css, "var(--fs-")
			if m := fixedHeading.FindString(css); m != "" {
				t.Errorf("%s/%s sizes a heading outside the scale: %q", slug, name, m[max(0, len(m)-80):])
			}
		}
		if uses == 0 {
			t.Errorf("theme %s never reads the type scale (var(--fs-*))", slug)
		}
	}
}
