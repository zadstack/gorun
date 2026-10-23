package theme

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/config"
)

func identityGet(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func identityVar(css, name string) string {
	m := regexp.MustCompile(regexp.QuoteMeta(name)+`:([^;}]+)`).FindAllStringSubmatch(css, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1] // the last declaration wins
}

// The dark scheme must redefine every surface and text colour the shared
// tokens declare, or a light value (a heading colour, a status background,
// a link hover) survives on the dark page. Only accent fills and the text
// printed on them may keep the theme's value.
func TestDarkSchemeRedefinesEveryLiteralColourToken(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "default", "assets", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	fills := map[string]bool{"--accent": true, "--accent-hover": true, "--on-accent": true}
	css := buildIdentity(identityGet(map[string]string{idScheme: "dark"})).CSS
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{3,8}|rgba?\()`).FindAllStringSubmatch(string(raw), -1) {
		name := m[1]
		if fills[name] {
			continue
		}
		if identityVar(css, name) == "" {
			t.Errorf("dark scheme leaves %s at its light value", name)
		}
	}
}

// Text in the accent colour (links, accent ink) must reach 4.5:1 on the page
// background the identity produces, while fills keep the chosen accent.
func TestAccentTextIsReadableOnTheIdentityBackground(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		bg     string
	}{
		{"amber on the light fallback", map[string]string{idAccent: "#f5b800"}, identityLightFallback},
		{"dark green on the dark scheme", map[string]string{idAccent: "#167252", idScheme: "dark"}, identityDarkBackground},
		{"navy on a dark custom background", map[string]string{idAccent: "#1d3b8a", idBackground: "#0b1020"}, "#0b1020"},
		{"readable accent stays itself", map[string]string{idAccent: "#0b5394", idBackground: "#ffffff"}, "#ffffff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			css := buildIdentity(identityGet(tc.values)).CSS
			if got := identityVar(css, "--accent"); got != tc.values[idAccent] {
				t.Fatalf("--accent fill = %q, want the chosen %q", got, tc.values[idAccent])
			}
			for _, name := range []string{"--blue", "--blue-hover", "--link", "--accent-ink"} {
				ink := identityVar(css, name)
				if r := contrast(ink, tc.bg); r < minTextContrast {
					t.Errorf("%s = %s has contrast %.2f on %s", name, ink, r, tc.bg)
				}
			}
			if tc.name == "readable accent stays itself" && identityVar(css, "--link") != tc.values[idAccent] {
				t.Errorf("a readable accent must not be changed: --link = %s", identityVar(css, "--link"))
			}
		})
	}
}

// Saving dark text with the dark scheme must fail the contrast check: the
// rendered background is the dark palette's, not the theme's light one.
// Light text on the dark scheme, and dark text under a light builder
// background, pass.
func TestSaveContrastUsesTheRenderedBackground(t *testing.T) {
	e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = "default" })
	svc := e.svc
	ctx := context.Background()
	check := func(values map[string]string) bool {
		t, b := svc.effectiveTextBackground(ctx, IdentitySnapshot{Theme: "default", Values: values})
		return t == "" || contrast(t, b) >= minTextContrast
	}
	if check(map[string]string{idScheme: "dark", idText: "#222222"}) {
		t.Error("dark text on the dark scheme passed the save check")
	}
	if !check(map[string]string{idScheme: "dark", idText: "#f5f5f5"}) {
		t.Error("light text on the dark scheme failed the save check")
	}
	if !check(map[string]string{idScheme: "dark"}) {
		t.Error("the dark scheme's own palette failed the save check")
	}
	e.setting("builder.identity", brand.Identity{Accent: "#167252", Background: "#fdfdfb", Text: "#172b29", Font: "zad", Width: "standard"})
	if !check(map[string]string{idText: "#333333"}) {
		t.Error("dark text over a light builder background failed the save check")
	}
	if check(map[string]string{idText: "#e0e0e0"}) {
		t.Error("light text over a light builder background passed the save check")
	}
}
