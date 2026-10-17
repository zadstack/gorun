package theme

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

func TestExtraThemesDirsFromEnv(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	sep := string(filepath.ListSeparator)
	t.Setenv(EnvThemesExtra, a+sep+" "+sep+b)
	if got := ExtraThemesDirs(); !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatalf("ExtraThemesDirs = %v", got)
	}
	t.Setenv(EnvThemesExtra, "")
	if got := ExtraThemesDirs(); len(got) != 0 {
		t.Fatalf("empty env gave %v", got)
	}
}

// A theme kept outside the core directory (a brand theme) lists, activates,
// inherits from a core parent and serves from its own directory; a core
// theme of the same slug wins over an extra one.
func TestExtraThemesDirLoadsBrandThemes(t *testing.T) {
	core, extra := t.TempDir(), t.TempDir()
	writeTheme(t, core, "default", "", map[string]string{
		"templates/index.html": `{{part "header" .}}core index{{part "footer" .}}`,
		"parts/header.html":    `<header>core header</header>`,
		"parts/footer.html":    `<footer>core footer</footer>`,
	})
	writeTheme(t, extra, "brand", `{"name":"Brand","version":"1.0.0","parent":"default"}`, map[string]string{
		"templates/index.html": `{{part "header" .}}brand index{{part "footer" .}}`,
		"assets/brand.css":     `body{}`,
	})
	writeTheme(t, extra, "default", `{"name":"Shadow","version":"1.0.0"}`, map[string]string{
		"templates/index.html": `shadow`,
	})
	t.Setenv(EnvThemesExtra, extra)
	e := newEnv(t, core)
	ctx := context.Background()

	list, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, info := range list {
		slugs = append(slugs, info.Slug+":"+info.Name)
	}
	if !reflect.DeepEqual(slugs, []string{"brand:Brand", "default:default"}) {
		t.Fatalf("List = %v", slugs)
	}
	info, err := e.svc.Lookup("brand")
	if err != nil || !strings.HasPrefix(info.Dir, extraAbs(t, extra)) {
		t.Fatalf("Lookup brand = %+v, %v", info, err)
	}
	if err := e.svc.Activate(ctx, "brand"); err != nil {
		t.Fatal(err)
	}
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), "core header", "brand index", "core footer")
	mustNotContain(t, rec.Body.String(), "shadow")
}

func extraAbs(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
