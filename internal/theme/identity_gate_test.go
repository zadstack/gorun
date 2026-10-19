package theme

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

var identityStyle = regexp.MustCompile(`<link rel="stylesheet" href="/site/identity\.css\?v=[0-9a-f]+" data-gorun-identity>`)

// extraThemes is a custom theme kept outside themes/ (see testdata/extra).
const extraThemes = "testdata/extra"

// identityThemes maps every shipped theme to whether it offers the site
// identity settings: only the catalog themes declare "identity" in supports;
// custom themes (sites with their own tokens) do not inherit it.
var identityThemes = map[string]bool{
	"default": true, "business": true, "magazine": true, "portfolio": true, "docs": true,
	"custom-fixture": false,
}

func TestIdentitySettingsOnlyForThemesThatDeclareIt(t *testing.T) {
	roots := []string{"../../themes", extraThemes}
	for slug, want := range identityThemes {
		info, err := findInfo(roots, slug)
		if err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		if got := supportsIdentity(info); got != want {
			t.Errorf("%s: supportsIdentity = %v, want %v (supports %v)", slug, got, want, info.Supports)
		}
		has := slices.ContainsFunc(settingsSchema(info), func(f content.Field) bool { return f.Key == idAccent })
		if has != want {
			t.Errorf("%s: identity fields in the settings schema = %v, want %v", slug, has, want)
		}
	}
}

func TestIdentityRenderedOnlyForThemesThatDeclareIt(t *testing.T) {
	t.Setenv(EnvThemesExtra, extraThemes)
	for slug, want := range identityThemes {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			e.setting(SettingKey(slug, idAccent), "#c2410c")
			e.setting(SettingKey(slug, idScheme), "dark")
			e.setting(SettingKey(slug, idCustomCSS), ".identity-probe{color:red}")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			n := len(identityStyle.FindAllString(body, -1))
			switch {
			case want && n != 1:
				t.Fatalf("want exactly one identity stylesheet link, got %d", n)
			case !want && n != 0:
				t.Fatalf("a theme without the identity capability printed %d identity links", n)
			case !want:
				mustNotContain(t, body, ".identity-probe", "--green:#c2410c", "/site/identity.css")
			}
		})
	}
}

// Two identity systems can be set at once: builder.identity (the site
// builder, printed by {{builderStyle}} as :root tokens) and the per-theme
// site identity (printed by {{identity .}}). Documented precedence
// (docs/THEMES.md → Site identity): per token, the site identity wins, the
// builder identity supplies the tokens it leaves empty, and the theme's own
// tokens fill the rest. The engine guarantees it by order and specificity:
// the identity block comes after the builder block in every catalog head
// and uses :root:root[data-theme] (0,3,0) against the builder's :root (0,1,0).
func TestSiteIdentityOutranksBuilderIdentity(t *testing.T) {
	for slug, ok := range identityThemes {
		if !ok {
			continue
		}
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			e.setting("builder.identity", map[string]string{"accent": "#167252", "background": "#fafafa", "text": "#111111", "font": "system", "width": "wide"})
			e.setting(SettingKey(slug, idAccent), "#c2410c")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			builder := strings.Index(body, `data-gorun-builder-identity`)
			site := identityStyle.FindStringIndex(body)
			if builder < 0 || site == nil {
				t.Fatalf("both identity links must render (builder at %d, site %v)", builder, site)
			}
			if site[0] < builder {
				t.Fatal("the site identity link must come after the builder identity link")
			}
			siteCSS := IdentityStylesheet(func(k string) string {
				switch k {
				case idAccent:
					return "#c2410c"
				default:
					return ""
				}
			})
			mustContain(t, siteCSS, ":root:root[data-theme]{", "--green:#c2410c;", "--gp-accent:#c2410c;")
			// Tokens the site identity leaves empty keep the builder's values.
			mustNotContain(t, siteCSS, "--bg:", "--ink:")
			mustContain(t, body, `/site/builder-identity.css?v=`)
		})
	}
}
