package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"golang.org/x/net/html"
)

func TestPreviewStylesheetBoundary(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"/themes/custom/assets/design.css?v=3", true},
		{"https://example.test/themes/custom/assets/design.css", false},
		{"//example.test/design.css", false}, {"/admin/private.css", false},
		{"/themes/other/assets/design.css", false}, {"/themes/custom/assets/../private.css", false},
		{"/themes/custom/assets/%2e%2e/private.css", false}, {"/themes/custom/assets/design.js", false},
		{"/themes/custom/assets/design.css#fragment", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			n := &html.Node{Attr: []html.Attribute{{Key: "href", Val: tc.url}, {Key: "rel", Val: "stylesheet"}}}
			if got := previewStylesheet(n, []Info{{Slug: "custom"}}); got != tc.want {
				t.Fatalf("allowed=%v", got)
			}
		})
	}
}

func TestPreviewKeepsLiveStylesheetCascadeOrder(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "ACTIVE"})
	writeTheme(t, root, "custom", `{"name":"Custom","parent":"default"}`, map[string]string{"templates/page-builder.html": `<!doctype html><html><head><link rel="stylesheet" href="/themes/custom/assets/site.css"><link rel="stylesheet" href="/themes/custom/assets/final.css"><link rel="stylesheet" href="/static/builder/builder.css"><style>{{builderStyle}}</style></head><body>{{builderBody .Content}}</body></html>`, "assets/site.css": ".first-theme{color:red}", "assets/final.css": ".last-theme{color:blue}"})
	e := newEnv(t, root)
	body, err := e.svc.RenderBuilderThemePreview(t.Context(), &content.Item{Type: "page", Slug: "home", Template: "builder", Body: "<h1>Draft</h1>", Locale: "en"}, BuilderPreviewOptions{Theme: "custom", Identity: brand.Default(), CSS: ".builder-base{display:block}"})
	if err != nil {
		t.Fatal(err)
	}
	first, final, base := strings.Index(body, ".first-theme"), strings.Index(body, ".last-theme"), strings.Index(body, ".builder-base")
	if first < 0 || final <= first || base <= final {
		t.Fatal("live stylesheet order changed")
	}
	if strings.Count(body, ".first-theme") != 1 || strings.Count(body, ".builder-base") != 1 {
		t.Fatal("preview duplicated or appended theme styles")
	}
}

func TestPreviewEmbedsLocalFontsAndConfinesAssets(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "fonts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "fonts", "face.woff2"), []byte("public-font"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "style.css"), []byte(`@font-face{font-family:Fixture;src:url("fonts/face.woff2")} .remote{background:url(https://example.test/image.png)}`), 0600); err != nil {
		t.Fatal(err)
	}
	chain := []Info{{Slug: "custom", Dir: root}}
	budget := 0
	css, err := inlinePreviewCSS("/themes/custom/assets/style.css", chain, &budget)
	if err != nil || !strings.Contains(css, "data:font/woff2;base64,cHVibGljLWZvbnQ=") || strings.Contains(css, `url("fonts/face.woff2")`) {
		t.Fatalf("font not self contained: %v", err)
	}
	secret := filepath.Join(t.TempDir(), "private.woff2")
	if err := os.WriteFile(secret, []byte("never-render-this"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(assets, "escape.woff2")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreviewAsset("/themes/custom/assets/escape.woff2", chain, &budget); err == nil {
		t.Fatal("symlink escaped public asset root")
	}
	if _, err := readPreviewAsset("/themes/custom/assets/../private.woff2", chain, &budget); err == nil {
		t.Fatal("traversal allowed")
	}
	budget = previewTotalLimit
	if _, err := readPreviewAsset("/themes/custom/assets/style.css", chain, &budget); err == nil {
		t.Fatal("total asset limit ignored")
	}
	large := filepath.Join(assets, "large.woff2")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(previewAssetLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	budget = 0
	if _, err := readPreviewAsset("/themes/custom/assets/large.woff2", chain, &budget); err == nil {
		t.Fatal("individual asset limit ignored")
	}
}

func TestEmbeddedZadFontPreview(t *testing.T) {
	budget := 0
	css, err := inlinePreviewCSS("/static/admin/zad-font.css", nil, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(css, "data:font/woff2;base64,") != 5 {
		t.Fatal("preview missing embedded font weights")
	}
	for _, href := range []string{"/static/admin/admin.js", "/static/admin/fonts/zad/../secret.woff2", "https://example.test/zad-font.css"} {
		if _, err := readPreviewAsset(href, nil, &budget); err == nil {
			t.Fatalf("allowed %s", href)
		}
	}
}
