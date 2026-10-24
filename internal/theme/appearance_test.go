package theme

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
)

func TestAppearanceUsesActiveThemeInheritanceWithoutWrites(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Parent","version":"1.0"}`, map[string]string{"templates/index.html": "parent", "assets/tokens.css": `:where(:root){--bg:#fff;--ink:#111;--accent:#a00;--font:Parent,sans-serif;--font-heading:Parent,serif;}`})
	writeTheme(t, root, "child", `{"name":"Child","version":"2.0","parent":"default"}`, map[string]string{"assets/style.css": `:where(html[data-theme="child"]){--accent:#123456;--font-heading:Child,serif;}`})
	e := newEnv(t, root)
	e.setting(SettingTheme, "child")
	e.setting(SettingKey("child", idAccent), "#abcdef") // child does not opt in
	e.setting("theme.child.private_api_key", "never-publish-this")
	got := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, got.Theme.Slug, "child", "settings:site.theme")
	assertAppearanceValue(t, got.Colors["accent"], "#123456", "theme:child:css-token:--accent")
	assertAppearanceValue(t, got.Colors["background"], "#fff", "theme:default:css-token:--bg")
	assertAppearanceValue(t, got.Fonts["heading"], "Child,serif", "theme:child:css-token:--font-heading")
	if got.Colors["warning"].Available || got.Colors["warning"].Value != nil || got.Colors["warning"].Reason == "" {
		t.Fatalf("missing color presented as available: %+v", got.Colors["warning"])
	}
	if got.ObservedAt.IsZero() || len(got.Limitations) == 0 {
		t.Fatalf("missing observation provenance: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{root, "private_api_key", "never-publish-this", "#abcdef"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("public appearance leaked %q: %s", forbidden, encoded)
		}
	}
	if value := e.app.Deps.Settings.GetString(context.Background(), SettingKey("child", idAccent), ""); value != "#abcdef" {
		t.Fatalf("discovery changed identity: %q", value)
	}
}

func TestAppearanceIdentityOverridesRequireSupportAndStayFresh(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Identity","version":"1","supports":["identity"]}`, map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--bg:#fff;--ink:#000;--accent:#111;--font:Example,sans-serif;}`})
	e := newEnv(t, root)
	e.setting(SettingKey("default", idScheme), "dark")
	e.setting(SettingKey("default", idAccent), "#123456")
	e.setting(SettingKey("default", idBody), "zad")
	got := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, got.Colors["background"], "#111418", "settings:theme.default.identity_scheme")
	assertAppearanceValue(t, got.Colors["accent"], "#123456", "settings:theme.default.identity_accent")
	assertAppearanceValue(t, got.Fonts["body"], "Zad,'Zad Font',Tahoma,sans-serif", "settings:theme.default.identity_body_font")
	e.setting(SettingKey("default", idAccent), "#654321")
	next := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, next.Colors["accent"], "#654321", "settings:theme.default.identity_accent")
	if !next.ObservedAt.Equal(got.ObservedAt) {
		t.Fatal("unchanged theme files were scanned again")
	}
	e.setting(SettingKey("default", idAccent), "red;}body{display:none")
	invalid := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, invalid.Colors["accent"], "#111", "theme:default:css-token:--accent")
}

func TestAppearanceReportsCustomCSSAndBuilderAmbiguity(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Identity","supports":["identity"]}`, map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--bg:#fff;--accent:#111;--font:Example,sans-serif;}`})
	e := newEnv(t, root)
	for _, mode := range []string{"custom_css", "builder_identity"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "custom_css" {
				e.setting(SettingKey("default", idCustomCSS), `body{color:red}`)
			} else {
				e.setting(SettingKey("default", idCustomCSS), "")
				e.setting("builder.identity", brand.Default())
			}
			got := e.svc.Appearance(context.Background())
			for _, value := range []AppearanceValue{got.Colors["background"], got.Colors["accent"], got.Fonts["body"]} {
				if value.Available || value.Value != nil || !strings.Contains(value.Reason, mode) {
					t.Errorf("ambiguous appearance presented as effective: %+v", value)
				}
			}
		})
	}
}

func TestAppearanceCacheIsBoundedCopiedAndInvalidated(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--accent:#111}`})
	e := newEnv(t, root)
	first := e.svc.Appearance(context.Background())
	*first.Colors["accent"].Value = "#bad"
	first.Colors["accent"] = AppearanceValue{}
	if err := os.WriteFile(filepath.Join(root, "default/assets/style.css"), []byte(`:root{--accent:#222}`), 0o644); err != nil {
		t.Fatal(err)
	}
	assertAppearanceValue(t, e.svc.Appearance(context.Background()).Colors["accent"], "#111", "theme:default:css-token:--accent")
	e.svc.InvalidateCache()
	assertAppearanceValue(t, e.svc.Appearance(context.Background()).Colors["accent"], "#222", "theme:default:css-token:--accent")
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.svc.Appearance(context.Background()) }()
	}
	wg.Wait()
}

func TestAppearanceMissingThemeIsExplicit(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x"})
	e := newEnv(t, root)
	if err := os.RemoveAll(filepath.Join(root, "default")); err != nil {
		t.Fatal(err)
	}
	got := e.svc.Appearance(context.Background())
	if got.Theme.Name.Available || got.Theme.Name.Value != nil || got.Theme.Name.Reason == "" || got.Colors["accent"].Available {
		t.Fatalf("missing theme looks available: %+v", got)
	}
}

func assertAppearanceValue(t *testing.T, got AppearanceValue, value, source string) {
	t.Helper()
	if !got.Available || got.Value == nil || *got.Value != value || got.Source != source {
		t.Fatalf("appearance value = %+v, want %q from %q", got, value, source)
	}
}

func TestAppearanceConditionalTokensAreUnknown(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--accent:#111;--font:Base,sans-serif;--font-heading:Heading,serif;} @media(prefers-color-scheme:dark){:root{--accent:#222;}} :where(html[data-theme="default"][dir="rtl"]){--font-heading:Arabic,serif;}`})
	e := newEnv(t, root)
	report := e.svc.Appearance(context.Background())
	for _, value := range []AppearanceValue{report.Colors["accent"], report.Fonts["heading"]} {
		if value.Available || value.Value != nil || value.Reason != "conditional_css_requires_page_evaluation" {
			t.Fatalf("conditional token presented as universal: %+v", value)
		}
	}
	assertAppearanceValue(t, report.Fonts["body"], "Base,sans-serif", "theme:default:css-token:--font")
}

func TestAppearanceShippedDefaultDeclarations(t *testing.T) {
	e := newEnv(t, "../../themes")
	report := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, report.Colors["background"], "#f5f7fa", "theme:default:css-token:--bg")
	if report.Fonts["body"].Value == nil || !strings.Contains(*report.Fonts["body"].Value, "Zad") {
		t.Fatalf("shipped font unavailable: %+v", report.Fonts)
	}
	if report.Fonts["heading"].Available {
		t.Fatalf("locale-dependent heading font reported as universal: %+v", report.Fonts["heading"])
	}
}

func TestAppearanceMalformedSettingsAreUnavailable(t *testing.T) {
	for _, key := range []string{"theme.default.identity_accent", "builder.identity"} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			writeTheme(t, root, "default", `{"name":"Default","supports":["identity"]}`, map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--accent:#111}`})
			e := newEnv(t, root)
			e.setting(key, 12)
			report := e.svc.Appearance(context.Background())
			if value := report.Colors["accent"]; value.Available || value.Value != nil || value.Reason != "settings_read_failed" || value.Source != "settings:"+key {
				t.Fatalf("invalid setting silently became success: %+v", value)
			}
		})
	}
}

func TestAppearanceConditionalAliasCannotClaimKnownFont(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--font:var(--family);--family:Base,sans-serif;} @media(prefers-color-scheme:dark){:root{--family:Conditional,serif;}}`})
	e := newEnv(t, root)
	value := e.svc.Appearance(context.Background()).Fonts["body"]
	if value.Available || value.Reason != "conditional_css_requires_page_evaluation" {
		t.Fatalf("conditional alias presented as known: %+v", value)
	}
}

func TestAppearancePrefersCanonicalColorsOverSemanticVariants(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--accent:#123456;--on-accent:#fff;--ink:#111;--accent-ink:#999;--zs-accent:#abcdef;--accent-background:#aaa;--bg:#fafafa;}`})
	e := newEnv(t, root)
	report := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, report.Colors["accent"], "#123456", "theme:default:css-token:--accent")
	assertAppearanceValue(t, report.Colors["text"], "#111", "theme:default:css-token:--ink")
	assertAppearanceValue(t, report.Colors["background"], "#fafafa", "theme:default:css-token:--bg")
}

func TestAppearanceSafeBrandAliasesExcludeSemanticVariants(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--zs-accent:#123456;--on-accent:#fff;--zs-ink:#111;--accent-ink:#999;}`})
	e := newEnv(t, root)
	report := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, report.Colors["accent"], "#123456", "theme:default:css-token:--zs-accent")
	assertAppearanceValue(t, report.Colors["text"], "#111", "theme:default:css-token:--zs-ink")
}

func TestAppearanceRejectsInvalidThemeIdentifiersWithoutDisclosure(t *testing.T) {
	for _, invalid := range []string{"/srv/private/client-themes/acme", "../private-client-theme", "private client theme"} {
		t.Run(invalid, func(t *testing.T) {
			root := t.TempDir()
			writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x"})
			e := newEnv(t, root)
			e.setting(SettingTheme, invalid)
			report := e.svc.Appearance(context.Background())
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), invalid) {
				t.Fatalf("invalid identifier leaked in public appearance: %s", encoded)
			}
			for _, value := range []AppearanceValue{report.Theme.Slug, report.Theme.Name, report.Theme.Version, report.Colors["accent"], report.Fonts["body"]} {
				if value.Available || value.Value != nil || value.Reason != "invalid_theme_identifier" {
					t.Fatalf("invalid theme must be explicitly unavailable: %+v", value)
				}
			}
			if stored := e.app.Deps.Settings.GetString(context.Background(), SettingTheme, ""); stored != invalid {
				t.Fatalf("discovery changed theme setting: %q", stored)
			}
		})
	}
}

func TestAppearanceMissingValidThemeIdentifierRemainsPublic(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x"})
	e := newEnv(t, root)
	e.setting(SettingTheme, "missing-theme")
	report := e.svc.Appearance(context.Background())
	assertAppearanceValue(t, report.Theme.Slug, "missing-theme", "settings:site.theme")
	if report.Theme.Name.Available || report.Theme.Version.Available || report.Colors["accent"].Available {
		t.Fatalf("missing valid theme must not acquire metadata: %+v", report.Theme)
	}
}

func TestAppearanceEmptyStoredThemeSelectionKeepsSettingsProvenance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{{"empty", ""}, {"null", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "x"})
			e := newEnv(t, root)
			e.setting(SettingTheme, tc.value)
			report := e.svc.Appearance(context.Background())
			for _, value := range []AppearanceValue{report.Theme.Slug, report.Theme.Name, report.Theme.Version} {
				if value.Source != "settings:site.theme" || value.Available || value.Reason != "invalid_theme_identifier" {
					t.Fatalf("empty stored selection must retain settings provenance: %+v", value)
				}
			}
			var stored any
			if err := e.app.Deps.Settings.Get(context.Background(), SettingTheme, &stored); err != nil || stored != tc.value {
				t.Fatalf("discovery changed empty selection: %#v, %v", stored, err)
			}
		})
	}
}

func TestAppearanceSeparatesDeclaredFromEffectiveValues(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Identity","version":"1","supports":["identity"],"logos":{"icon":"icon.svg"}}`, map[string]string{"templates/index.html": "x", "assets/icon.svg": "<svg/>", "assets/style.css": `:root{--bg:#fff;--ink:#000;--accent:#111;--font:Example,sans-serif;}`})
	e := newEnv(t, root)
	got := e.svc.Appearance(context.Background())
	if got.Declared == nil || len(got.Overrides) != 0 || got.Scheme.Available || got.Radius.Available || got.CustomCSS.Present || got.CustomCSS.Status != "none" {
		t.Fatalf("untouched theme reported overrides: %+v", got)
	}
	assertAppearanceValue(t, got.Declared.Colors["accent"], "#111", "theme:default:css-token:--accent")
	assertAppearanceValue(t, got.Colors["accent"], "#111", "theme:default:css-token:--accent")
	e.setting(SettingKey("default", idAccent), "#123456")
	e.setting(SettingKey("default", idBody), "zad")
	e.setting(SettingKey("default", idScheme), "light")
	e.setting(SettingKey("default", idRadius), "round")
	got = e.svc.Appearance(context.Background())
	assertAppearanceValue(t, got.Declared.Colors["accent"], "#111", "theme:default:css-token:--accent")
	assertAppearanceValue(t, got.Colors["accent"], "#123456", "settings:theme.default.identity_accent")
	assertAppearanceValue(t, got.Declared.Fonts["body"], "Example,sans-serif", "theme:default:css-token:--font")
	assertAppearanceValue(t, got.Scheme, "light", "settings:theme.default.identity_scheme")
	assertAppearanceValue(t, got.Radius, "16px", "settings:theme.default.identity_radius")
	byRole := map[string]AppearanceOverride{}
	for _, o := range got.Overrides {
		byRole[o.Role] = o
	}
	accent, ok := byRole["colors.accent"]
	if !ok || accent.Value != "#123456" || accent.Declared == nil || *accent.Declared != "#111" || accent.Source != "settings:theme.default.identity_accent" {
		t.Fatalf("accent override: %+v", accent)
	}
	if body, ok := byRole["fonts.body"]; !ok || body.Declared == nil || *body.Declared != "Example,sans-serif" {
		t.Fatalf("body override: %+v", byRole)
	}
	if heading, ok := byRole["fonts.heading"]; ok {
		t.Fatalf("unset font reported as override: %+v", heading)
	}
	// Custom CSS is reported with its size and makes effective values unavailable.
	e.setting(SettingKey("default", idCustomCSS), "body{color:red}")
	got = e.svc.Appearance(context.Background())
	if !got.CustomCSS.Present || got.CustomCSS.Bytes != 15 || got.CustomCSS.Status != "applied_requires_browser_evaluation" || got.Colors["accent"].Available {
		t.Fatalf("%+v %+v", got.CustomCSS, got.Colors["accent"])
	}
	if got.Declared.Colors["accent"].Value == nil {
		t.Fatal("custom css erased the declared layer")
	}
	e.setting(SettingKey("default", idCustomCSS), "@import url(x)")
	got = e.svc.Appearance(context.Background())
	if got.CustomCSS.Status != "invalid_not_applied" || !got.Colors["accent"].Available {
		t.Fatalf("%+v", got.CustomCSS)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "body{color:red}") {
		t.Fatal("report leaked a path or the custom stylesheet body")
	}
	if info := e.svc.Active(context.Background()); info.Logos["icon"] != "icon.svg" {
		t.Fatalf("manifest logos not loaded: %+v", info.Logos)
	}
}

func TestThemeManifestLogosMustStayUnderAssets(t *testing.T) {
	for _, bad := range []string{`{"name":"x","version":"1","logos":{"icon":"../secret.svg"}}`, `{"name":"x","version":"1","logos":{"icon":"/etc/passwd"}}`, `{"name":"x","version":"1","logos":{"banner":"icon.svg"}}`, `{"name":"x","version":"1","logos":{"icon":"icon.svg?x=1"}}`} {
		root := t.TempDir()
		writeTheme(t, root, "default", bad, map[string]string{"templates/index.html": "x"})
		if _, err := loadInfo(root, "default"); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if !ValidAssetPath("marks/light.svg") || ValidAssetPath("") || ValidAssetPath("a/../b.svg") {
		t.Fatal("asset path rule")
	}
}

// TestAppearanceLinkRoleIsResolvedInk: Appearance reports the link role as the
// derived readable ink the renderer actually emits for --link (against the
// effective identity background), not the raw accent, while the accent role
// stays the raw accent.
func TestAppearanceLinkRoleIsResolvedInk(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Identity","version":"1","supports":["identity"]}`,
		map[string]string{"templates/index.html": "x", "assets/style.css": `:root{--bg:#fff;--ink:#000;--accent:#111;}`})
	e := newEnv(t, root)
	e.setting(SettingKey("default", idAccent), "#f5b800") // a light accent: readable ink differs from the fill
	rep := e.svc.Appearance(context.Background())

	wantInk := identityVar(buildIdentity(identityGet(map[string]string{idAccent: "#f5b800"})).CSS, "--link")
	if wantInk == "" || wantInk == "#f5b800" {
		t.Fatalf("expected a derived link ink distinct from the accent, got %q", wantInk)
	}
	assertAppearanceValue(t, rep.Colors["link"], wantInk, "settings:theme.default.identity_accent")
	assertAppearanceValue(t, rep.Colors["accent"], "#f5b800", "settings:theme.default.identity_accent")
}
