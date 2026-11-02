package theme

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
)

func TestStandaloneIdentityNilDepsAndInvalidBrandFallBack(t *testing.T) {
	want := StandaloneBrand(brand.Default())
	for _, got := range []StandaloneIdentity{
		StandaloneIdentityFor(context.Background(), nil),
		StandaloneBrand(brand.Identity{Accent: `</style><script>bad()</script>`, Font: "bad"}),
	} {
		if got.CSS != want.CSS || strings.Join(got.FontStylesheets, ",") != strings.Join(want.FontStylesheets, ",") {
			t.Fatal("standalone pages must remain readable without a valid identity")
		}
	}
}

func TestStandaloneIdentityUsesPublishedThemeWithoutDraftOrCustomCSS(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Brand","version":"1","supports":["identity"]}`, map[string]string{
		"templates/index.html": "x",
		"assets/style.css":     `:root{--bg:#fdfdfb;--ink:#172b29;--accent:#124536;}`,
	})
	e := newEnv(t, root)
	e.setting("builder.identity", brand.Identity{Accent: "#167252", Background: "#fdfdfb", Text: "#172b29", Font: "mono", Width: "standard"})
	e.setting("theme.default.identity_accent", "#f5b800")
	e.setting("theme.default.identity_body_font", "readex")
	e.setting("theme.default.identity_latin_font", "source-serif")
	e.setting("theme.default.identity_heading_font", "zad")
	e.setting("theme.default.custom_css", ".private-note{color:red}")
	e.setting("theme.default.identity_draft", map[string]any{"snapshot": map[string]any{"values": map[string]string{"identity_accent": "#abcdef", "identity_body_font": "amiri"}}})
	got := e.svc.StandaloneIdentity(context.Background())
	if fill := identityVar(got.CSS, "--standalone-accent"); fill != "#f5b800" {
		t.Fatalf("published accent did not override builder: %s", fill)
	}
	if contrast(identityVar(got.CSS, "--standalone-link"), identityVar(got.CSS, "--standalone-bg")) < minTextContrast {
		t.Fatal("amber links need a readable ink independent from the fill")
	}
	if contrast(identityVar(got.CSS, "--standalone-on-accent"), "#f5b800") < minTextContrast {
		t.Fatal("accent label is unreadable")
	}
	for _, bad := range []string{"#abcdef", "Amiri", ".private-note", "[data-theme]"} {
		if strings.Contains(got.CSS+strings.Join(got.FontStylesheets, ""), bad) {
			t.Errorf("standalone identity leaked unwanted styling %q", bad)
		}
	}
	if !strings.Contains(strings.Join(got.FontStylesheets, ""), "/static/admin/zad-font.css") {
		t.Fatal("theme Zad heading must load its bundled font even when builder body font is mono")
	}
	if !strings.Contains(got.CSS, "--font:'Readex Pro'") || !strings.Contains(strings.Join(got.FontStylesheets, ""), "Readex+Pro") || !strings.Contains(strings.Join(got.FontStylesheets, ""), "Source+Serif+4") {
		t.Fatal("validated published fonts must carry their font assets")
	}
}

func TestStandaloneIdentityRejectsUnsafeValuesAndUnreadableTheme(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Brand","version":"1","supports":["identity"]}`, map[string]string{
		"templates/index.html": "x",
		"assets/style.css":     `:root{--bg:#eee;--ink:#eee;--accent:#357;}`,
	})
	e := newEnv(t, root)
	e.setting("theme.default.identity_body_font", `bad;}</style><script>bad()</script>`)
	e.setting("theme.default.identity_accent", `red;}</style><script>bad()</script>`)
	got := e.svc.StandaloneIdentity(context.Background())
	if contrast(identityVar(got.CSS, "--standalone-ink"), identityVar(got.CSS, "--standalone-bg")) < minTextContrast {
		t.Fatal("low-contrast theme must use safe fallback text and background")
	}
	if strings.Contains(got.CSS, "<script>") || strings.Contains(got.CSS, "bad;") {
		t.Fatal("settings injected raw style content")
	}
}
