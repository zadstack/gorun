package pattern

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
	"github.com/zadstack/gorun/pkg/patternkit"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

func TestBundledPatternsValidate(t *testing.T) {
	root := filepath.Join("..", "..", "patterns")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"editorial": true, "company": true, "showcase": true, "guidebook": true}
	found := 0
	for _, e := range entries {
		if !e.IsDir() || !want[e.Name()] {
			continue
		}
		found++
		dir := filepath.Join(root, e.Name())
		if issues := patternkit.ValidateDir(dir); len(issues) != 0 {
			t.Errorf("%s: %v", e.Name(), issues)
		}
	}
	if found != len(want) {
		t.Fatalf("found %d bundled patterns, want %d", found, len(want))
	}
}

func TestPatternLayerOverridesFrontPage(t *testing.T) {
	themes := t.TempDir()
	writeTheme(t, themes, "default", `{"name":"Default","version":"1.0.0","supports":["rtl","identity"]}`, map[string]string{
		"templates/index.html":      `THEME-INDEX`,
		"templates/front-page.html": `THEME-FRONT`,
		"parts/header.html":         `<header>H</header>`,
		"parts/footer.html":         `<footer>F</footer>`,
	})
	patterns := t.TempDir()
	writePattern(t, patterns, "editorial", map[string]string{
		"templates/front-page.html": `PATTERN-FRONT {{t "pat.editorial.eyebrow"}}`,
		"lang/en.json":              `{"pat.editorial.eyebrow":"This edition"}`,
		"lang/ar.json":              `{"pat.editorial.eyebrow":"هذا العدد"}`,
	})

	cfg := config.Config{
		Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32),
		BaseURL: "http://example.test", Theme: "default",
	}
	themeMod := theme.New(theme.WithThemesDir(themes))
	patMod := New(WithPatternsDir(patterns))
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{
		settingsmod.New(), auth.New(), fake.NewModule(), themeMod, patMod,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)

	svc, ok := kernel.Get[theme.Service](app.Deps, kernel.ServiceTheme)
	if !ok {
		t.Fatal("theme service missing")
	}
	pat, ok := kernel.Get[Service](app.Deps, kernel.ServicePattern)
	if !ok {
		t.Fatal("pattern service missing")
	}

	body := renderHome(t, svc)
	if !strings.Contains(body, "THEME-FRONT") {
		t.Fatalf("without pattern want theme front, got %q", body)
	}

	if err := pat.Activate(context.Background(), "editorial"); err != nil {
		t.Fatal(err)
	}
	body = renderHome(t, svc)
	if !strings.Contains(body, "PATTERN-FRONT") || !strings.Contains(body, "هذا العدد") {
		t.Fatalf("with pattern want pattern front + ar copy, got %q", body)
	}
	files, err := svc.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.Name == "front-page.html" && f.Source == "pattern" {
			found = true
		}
	}
	if !found {
		t.Fatalf("front-page.html source want pattern, got %+v", files)
	}

	if err := pat.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	body = renderHome(t, svc)
	if !strings.Contains(body, "THEME-FRONT") {
		t.Fatalf("after clear want theme front, got %q", body)
	}
}

func renderHome(t *testing.T, svc theme.Service) string {
	t.Helper()
	rec := httptest.NewRecorder()
	tc := &theme.Context{
		Site: theme.SiteInfo{Title: "T", Locale: "ar"}, Locale: "ar", Dir: "rtl",
		Extra: map[string]any{},
	}
	if err := svc.Render(context.Background(), rec, kernel.KindRequest{Kind: theme.KindHome}, tc); err != nil {
		t.Fatal(err)
	}
	return rec.Body.String()
}

func writeTheme(t *testing.T, root, slug, meta string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	mustWrite(t, filepath.Join(dir, "theme.json"), meta)
	for rel, body := range files {
		mustWrite(t, filepath.Join(dir, rel), body)
	}
}

func writePattern(t *testing.T, root, slug string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	mustWrite(t, filepath.Join(dir, "pattern.json"), `{
  "kind":"pattern","slug":"`+slug+`","version":"1.0.0",
  "name":{"en":"`+slug+`","ar":"`+slug+`"},
  "description":{"en":"test","ar":"اختبار"}
}`)
	for rel, body := range files {
		mustWrite(t, filepath.Join(dir, rel), body)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
