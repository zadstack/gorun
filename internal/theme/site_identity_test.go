package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func identityGetter(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func TestIdentityCSSEmptyKeepsTheme(t *testing.T) {
	if got := identityCSS(identityGetter(nil), "n"); got != "" {
		t.Fatalf("no settings must render nothing, got %q", got)
	}
}

func TestIdentityCSSRendersTokensFontsAndCustomCSS(t *testing.T) {
	get := identityGetter(map[string]string{
		idAccent: "#c2410c", idScheme: "dark", idHeading: "readex", idBody: "zad", idRadius: "round",
		idCustomCSS: ".hero h1{letter-spacing:-.02em}",
	})
	css := IdentityStylesheet(get)
	for _, want := range []string{
		":root:root[data-theme]{",
		"--bg:#111418;", "color-scheme:dark;", "--focus-ring:#d7dee8;",
		"--green:#c2410c;", "--gp-accent:#c2410c;",
		"--font-heading:'Readex Pro'", "--font:Zad,",
		"--radius:16px;",
		":root:root[data-theme] body{background:var(--bg);color:var(--ink)}",
		".hero h1{letter-spacing:-.02em}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("identity stylesheet missing %q in:\n%s", want, css)
		}
	}
	got := string(identityCSS(get, "abc"))
	for _, want := range []string{
		`data-gorun-identity`,
		`/site/identity.css?v=`,
		"family=Readex+Pro:wght@400;500;600;700&amp;display=swap",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("identity HTML missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<style") {
		t.Error("identity must use a cached <link>, not an inline <style>")
	}
	if strings.Contains(got, "family=Zad") {
		t.Error("the bundled Zad font must not be requested from Google Fonts")
	}
}

func TestIdentityCSSRejectsUnsafeValues(t *testing.T) {
	got := string(identityCSS(identityGetter(map[string]string{
		idAccent:    "red;}body{display:none",
		idHeading:   "comic-sans",
		idRadius:    "huge",
		idCustomCSS: "</style><script>alert(1)</script>",
	}), "n"))
	if got != "" {
		t.Fatalf("invalid values must be ignored at render, got %q", got)
	}
}

func TestValidateIdentity(t *testing.T) {
	errs := validateIdentity(map[string]any{
		idAccent: "#12345", idBackground: "#fff", idText: "#0b1220",
		idCustomCSS: "@import url(x.css);",
	})
	if errs[idAccent] != "theme.identity.error_color" {
		t.Errorf("5-digit hex must be rejected: %v", errs)
	}
	if _, bad := errs[idBackground]; bad {
		t.Errorf("3-digit hex is valid: %v", errs)
	}
	if errs[idCustomCSS] != "theme.identity.error_css" {
		t.Errorf("@import must be rejected: %v", errs)
	}
	if e := validateIdentity(map[string]any{idCustomCSS: strings.Repeat("a", maxCustomCSS+1)}); e[idCustomCSS] != "theme.identity.error_css_size" {
		t.Errorf("oversized custom CSS must be rejected: %v", e)
	}
}

func TestSettingsSchemaAddsIdentityOnce(t *testing.T) {
	info := Info{Slug: "x", Supports: []string{"identity"}, Settings: []content.Field{{Key: "header_style", Type: "select"}, {Key: idAccent, Type: "text", Label: map[string]string{"en": "Brand"}}}}
	schema := settingsSchema(info)
	n := 0
	for _, f := range schema {
		if f.Key == idAccent {
			n++
			if f.Label["en"] != "Brand" {
				t.Error("a theme's own definition of an identity key must win")
			}
		}
	}
	if n != 1 {
		t.Fatalf("identity_accent appears %d times", n)
	}
	for _, k := range []string{idBackground, idText, idHeading, idBody, idScheme, idRadius, idCustomCSS} {
		found := false
		for _, f := range schema {
			found = found || f.Key == k
		}
		if !found {
			t.Errorf("schema lacks %s", k)
		}
	}
}

// An accent fill carries text; the identity derives --on-accent (white or
// near-black) that reaches WCAG AA 4.5:1 on the accent, for any valid accent.
func TestIdentityDerivesReadableOnAccent(t *testing.T) {
	for accent, want := range map[string]string{"#f59e0b": "#111111", "#1d4ed8": "#ffffff", "#c2410c": "#ffffff", "#ffff00": "#111111", "#777": "#000000"} {
		got := identityOnAccent(accent)
		if got != want {
			t.Errorf("identityOnAccent(%s) = %s, want %s", accent, got, want)
		}
		if r := contrast(accent, got); r < 4.5 {
			t.Errorf("identityOnAccent(%s) = %s has contrast %.2f < 4.5", accent, got, r)
		}
		css := IdentityStylesheet(identityGetter(map[string]string{idAccent: accent}))
		mustContain(t, css, "--on-accent:"+want+";", "--gp-on-accent:"+want+";")
	}
	// Exhaustive over a grid of accents: never below 4.5:1.
	const hex = "0123456789abcdef"
	for _, r := range hex {
		for _, g := range hex {
			for _, b := range hex {
				c := "#" + string([]rune{r, g, b})
				if got := identityOnAccent(c); contrast(c, got) < 4.5 {
					t.Fatalf("identityOnAccent(%s) = %s below 4.5:1", c, got)
				}
			}
		}
	}
	if css := string(identityCSS(identityGetter(map[string]string{idScheme: "dark"}), "n")); strings.Contains(css, "--on-accent") {
		t.Error("without an accent the theme's own on-accent color stays")
	}
}
