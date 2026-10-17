package theme

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/theme/fake"
)

// writeExtraTheme writes a theme.json-only child theme into dir.
func writeExtraTheme(t *testing.T, dir, slug, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// {{identity .}} through the real default head: printed exactly once for
// default and for a child that declares the capability (the child inherits
// the head, which must not duplicate the block), never for a child that does not.
func TestIdentityRendersOnceThroughTheDefaultHead(t *testing.T) {
	extra := t.TempDir()
	writeExtraTheme(t, extra, "acme", `{"name":"Acme","version":"1.0.0","parent":"default","supports":["rtl","identity"]}`)
	writeExtraTheme(t, extra, "acme-plain", `{"name":"Acme plain","version":"1.0.0","parent":"default","supports":["rtl"]}`)
	t.Setenv(EnvThemesExtra, extra)
	for slug, want := range map[string]int{"default": 1, "acme": 1, "acme-plain": 0} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			e.setting(SettingKey(slug, idAccent), "#c2410c")
			e.setting(SettingKey(slug, idRadius), "round")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			if n := len(identityStyle.FindAllString(body, -1)); n != want {
				t.Fatalf("identity <style> blocks = %d, want %d", n, want)
			}
			if want == 1 {
				mustContain(t, body, "--green:#c2410c;", "--on-accent:#ffffff;", "--radius:16px;")
				if strings.Index(body, "data-gorun-identity") > strings.Index(body, "</head>") {
					t.Fatal("the identity block must be inside <head>")
				}
			}
		})
	}
}

// The engine's empty commerce fallbacks let templates that call products
// and product parse and render without the commerce module.
func TestCommerceFallbacksParseAndRenderEmpty(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": `[{{with products}}groups{{end}}{{with product "x"}}one{{end}}{{range products}}r{{end}}]`})
	e := newEnv(t, root)
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("fallbacks must render nothing, got %q", got)
	}
}

func renderBusinessHome(t *testing.T, modules ...kernel.Module) string {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://example.test", Theme: "business"}
	mod := New(WithThemesDir("../../themes"), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	mods := append([]kernel.Module{settingsmod.New(), auth.New(), fake.NewModule()}, modules...)
	app, err := kernel.Boot(context.Background(), cfg, append(mods, mod))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	e := &env{t: t, app: app, svc: mod.svc, cfg: cfg}
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	mustNotContain(t, body, Marker500)
	mustContain(t, body, `class="sb-home"`)
	return body
}

// The business front page lists store services only when products exist.
func TestBusinessFrontPageServices(t *testing.T) {
	t.Run("without commerce", func(t *testing.T) {
		mustNotContain(t, renderBusinessHome(t), "sb-services-home", "/product/")
	})
	t.Run("with products", func(t *testing.T) {
		body := renderBusinessHome(t, &commerceStub{groups: stubGroups()})
		mustContain(t, body, `class="sb-services-home"`, "WordPress Starter", "n8n Starter", `/product/wp-monthly"`, `/shop">`, "Connect one site")
		if n := strings.Count(body, `class="sb-service-card"`); n != 4 {
			t.Fatalf("service cards = %d, want 4 (every product, at most 6)", n)
		}
	})
	t.Run("empty store", func(t *testing.T) {
		mustNotContain(t, renderBusinessHome(t, &commerceStub{}), "sb-services-home")
	})
}
