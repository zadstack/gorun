package theme

import (
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDeclaredVariantsListNativeFirst(t *testing.T) {
	info := Info{Supports: []string{"rtl", "header:framed", "header:rail", "footer:columns", "single:wide", "header:bogus", "archive:grid"}}
	if got := DeclaredVariants(info, "header"); !slices.Equal(got, []string{"native", "rail", "framed"}) {
		t.Fatalf("header = %v", got)
	}
	if got := DeclaredVariants(info, "footer"); !slices.Equal(got, []string{"native", "columns"}) {
		t.Fatalf("footer = %v", got)
	}
	if got := DeclaredVariants(info, "single"); !slices.Equal(got, []string{"native", "wide"}) {
		t.Fatalf("single = %v", got)
	}
	if got := DeclaredVariants(Info{Supports: []string{"rtl"}}, "archive"); !slices.Equal(got, []string{"native"}) {
		t.Fatalf("undeclared archive = %v", got)
	}
	if got := DeclaredVariants(info, "sidebar"); got != nil {
		t.Fatalf("unknown slot = %v", got)
	}
}

func TestResolveVariantFallsBackToNative(t *testing.T) {
	info := Info{Supports: []string{"header:rail", "single:centered"}}
	for _, tc := range []struct{ slot, stored, want string }{
		{"header", "rail", "rail"},
		{"header", "framed", "native"},
		{"header", `<script>alert(1)</script>`, "native"},
		{"header", "", "native"},
		{"single", "centered", "centered"},
		{"single", "wide", "native"},
		{"archive", "grid", "native"},
		{"sidebar", "x", "native"},
	} {
		if got := ResolveVariant(info, tc.slot, tc.stored); got != tc.want {
			t.Errorf("%s/%q = %q, want %q", tc.slot, tc.stored, got, tc.want)
		}
	}
}

// {{variant "header" .}} reads theme.<slug>.header_style and resolves it
// against the theme's own declarations; a child theme that declares nothing
// renders native even when a value is stored.
func TestVariantFuncUsesDeclarations(t *testing.T) {
	root := t.TempDir()
	files := baseTheme(map[string]string{"parts/header.html": `<html><body data-h="{{variant "header" .}}" data-s="{{variant "single" .}}">`})
	writeTheme(t, root, "default", `{"name":"Base","version":"1.0.0","locations":["primary"],"supports":["rtl","header:framed"]}`, files)
	writeTheme(t, root, "child", `{"name":"Child","version":"1.0.0","parent":"default","locations":["primary"],"supports":["rtl"]}`, map[string]string{})
	e := newEnv(t, root)
	e.setting("theme.default.header_style", "framed")
	e.setting("theme.default.single_style", "wide")
	e.setting("theme.child.header_style", "framed")
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `data-h="framed"`, `data-s="native"`)
	if err := e.svc.Activate(adminCtx(), "child"); err != nil {
		t.Fatal(err)
	}
	rec, err = e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `data-h="native"`)
}

// The Theme settings select for a *_style key offers only declared variants
// and says which are not offered by this theme.
func TestThemeSettingsVariantOptionsFollowDeclarations(t *testing.T) {
	root := t.TempDir()
	manifest := `{"name":"Base","version":"1.0.0","locations":["primary"],"supports":["rtl","header:rail"],"settings":[{"key":"header_style","type":"select","label":{"en":"Header layout"},"options":[{"value":"native","label":{"en":"Native"}},{"value":"rail","label":{"en":"Rail"}},{"value":"framed","label":{"en":"Framed"}}]}]}`
	writeTheme(t, root, "default", manifest, baseTheme(nil))
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "default" })
	c := e.client()
	e.login(c, "admin@example.com", "admin")
	_, page := c.do("GET", "/admin/appearance/theme-settings", nil, "Accept-Language", "en")
	mustContain(t, page, `value="rail"`, `value="native"`, "Not offered by this theme: Framed")
	_, ar := c.do("GET", "/admin/appearance/theme-settings", nil, "Accept-Language", "ar")
	mustContain(t, ar, "غير متاح في هذا القالب: Framed")
	if strings.Contains(page, `<option value="framed"`) {
		t.Fatal("undeclared variant offered as an option")
	}
	views := (&admin{s: e.svc}).settingViews(adminCtx(), settingsSchema(e.svc.Active(adminCtx())), map[string]any{"header_style": "framed"}, nil)
	for _, v := range views {
		if v.Key != "header_style" {
			continue
		}
		if v.Value != "framed" {
			t.Fatalf("stored value must be kept for display: %q", v.Value)
		}
		kept := false
		for _, o := range v.Options {
			kept = kept || (o.Value == "framed" && o.Selected && strings.HasPrefix(o.Label, "Framed — "))
		}
		if !kept {
			t.Fatalf("stored undeclared value must stay selected and labelled: %+v", v.Options)
		}
	}
}
