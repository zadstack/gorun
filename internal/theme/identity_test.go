package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme/fake"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContrastAndLevels(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		ratio float64
		level string
	}{
		{"#000", "#fff", 21, "aa"},
		{"#ffffff", "#000000ff", 21, "aa"},
		{"#777777", "#ffffff", 4.48, "aa_large"},
		{"rgb(16, 35, 61)", "#ffffff", 15.79, "aa"},
		{"#cccccc", "#ffffff", 1.61, "fail"},
	} {
		r := contrast(c.a, c.b)
		if r < c.ratio-0.05 || r > c.ratio+0.05 || level(r) != c.level {
			t.Errorf("%s on %s: %.2f %s", c.a, c.b, r, level(r))
		}
	}
	if contrast("var(--x)", "#fff") != 0 || contrast("#12", "#fff") != 0 {
		t.Error("unreadable colors must give 0")
	}
}

func TestRolesByName(t *testing.T) {
	toks := []designToken{{Name: "--green-soft", Value: "#dcefe7"}, {Name: "--green", Value: "#167252"}, {Name: "--bg", Value: "#f3f5f2"},
		{Name: "--ink", Value: "#141816"}, {Name: "--zs-link", Value: "#62185A"}, {Name: "--panel", Value: "#fff"}, {Name: "--line", Value: "#cbd3ce"}}
	got := map[string]string{}
	for _, r := range roles(toks) {
		got[r.Key] = r.Token.Name
	}
	// A child's color for a role wins over the parent's.
	if r := roles([]designToken{{Name: "--ink", Value: "#000"}, {Name: "--zs-ink", Value: "#15171E"}}); r[0].Token.Name != "--zs-ink" {
		t.Fatalf("parent color won: %+v", r)
	}
	want := map[string]string{"background": "--bg", "text": "--ink", "accent": "--green", "link": "--zs-link", "surface": "--panel", "line": "--line"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, got[k], v, got)
		}
	}
	rows := contrastRows(roles(toks))
	if len(rows) != 4 || rows[0].Key != "text_bg" || rows[len(rows)-1].Key != "on_accent" || rows[len(rows)-1].Level != "aa" {
		t.Fatalf("rows %+v", rows)
	}
	// A style is only built from safe values.
	if safeStyle("background: %s", "red;}</style><script>") != "" {
		t.Fatal("unsafe value styled")
	}
}

func TestThemeFonts(t *testing.T) {
	dir := t.TempDir()
	write := func(p, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644)
	}
	write("parts/header.html", `<link href="https://fonts.googleapis.com/css2?family=Almarai:wght@300;400;700&family=El+Messiri:wght@500;700&display=swap" rel="stylesheet">`)
	write("assets/style.css", `.prose h2 { font-family: "El Messiri", serif; } code { font-family: ui-monospace, monospace; }`)
	s := &service{root: dir}
	f := s.fonts([]Info{{Dir: dir}}, nil)
	if strings.Join(f.Families, ",") != "Almarai,El Messiri" || len(f.Links) != 1 || !strings.Contains(f.Links[0], "Almarai:wght@300;400;700") {
		t.Fatalf("families %v links %v", f.Families, f.Links)
	}
	if f.Heading != `"El Messiri", serif` || !strings.HasPrefix(f.Body, "Almarai,") {
		t.Fatalf("heading %q body %q", f.Heading, f.Body)
	}
}

// brandImg keeps the plain <img> markup when no distinct dark logo exists,
// switches to a <picture> with a dark-mode source when one does, prefers
// the dark mark when the site identity forces dark, and favicon follows the
// site icon, then the theme manifest, then nothing.
func TestBrandImgAndFaviconFollowLogoVariants(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Brand","version":"1","supports":["identity"],"logos":{"icon":"icon.svg"}}`, map[string]string{
		"templates/index.html": `{{with logo "primary"}}{{brandImg . "thumbnail" "mark"}}{{else}}NOLOGO{{end}}|{{favicon}}`,
		"assets/icon.svg":      "<svg/>",
	})
	e := newEnv(t, root)
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	store, ok := ms.(*fake.Media)
	if !ok {
		t.Fatal("fake media service expected")
	}
	add := func(id, file string) *media.Media {
		m := media.Media{ID: id, Filename: file, URL: "/media/2026/10/" + file, Mime: "image/png", Width: 64, Height: 64, Variants: map[string]media.Variant{"thumbnail": {URL: "/media/2026/10/thumb-" + file, Width: 32, Height: 32}}}
		store.Add(m)
		return &m
	}
	primary, dark, icon := add("01PRIMARY", "logo.png"), add("01DARK", "logo-dark.png"), add("01ICON", "icon.png")
	_ = context.Background()
	body := func() string {
		rec, err := e.render(kernel.KindRequest{Kind: "index"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return rec.Body.String()
	}
	if got := body(); !strings.Contains(got, "NOLOGO|") || !strings.Contains(got, `<link rel="icon" href="/themes/default/assets/icon.svg">`) {
		t.Fatalf("empty settings: %s", got)
	}
	e.setting("site.logo", primary.ID)
	got := body()
	if !strings.Contains(got, `<img src="`+primary.Variants["thumbnail"].URL+`"`) || strings.Contains(got, "<picture") {
		t.Fatalf("primary only must keep the plain img: %s", got)
	}
	e.setting("site.logo_dark", dark.ID)
	got = body()
	if !strings.Contains(got, `<picture><source srcset="`+dark.Variants["thumbnail"].URL+`" media="(prefers-color-scheme: dark)"><img src="`+primary.Variants["thumbnail"].URL+`"`) {
		t.Fatalf("dark variant: %s", got)
	}
	e.setting(SettingKey("default", idScheme), "dark")
	got = body()
	if !strings.Contains(got, `<source srcset="`+primary.Variants["thumbnail"].URL+`" media="(prefers-color-scheme: light)"><img src="`+dark.Variants["thumbnail"].URL+`"`) {
		t.Fatalf("forced dark scheme: %s", got)
	}
	e.setting("site.icon", icon.ID)
	if got = body(); !strings.Contains(got, `<link rel="icon" href="`+icon.URL+`"`) || strings.Contains(got, "icon.svg") {
		t.Fatalf("site icon must outrank the manifest: %s", got)
	}
	// Dark pointing at the same file as primary is not a variant.
	e.setting("site.logo_dark", primary.ID)
	if got = body(); strings.Contains(got, "<picture") {
		t.Fatalf("identical dark logo rendered a picture: %s", got)
	}
}
