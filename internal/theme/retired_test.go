package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// retiredRoot is a themes directory holding only the general catalog.
func retiredRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, slug := range []string{"default", "business", "magazine", "portfolio"} {
		writeTheme(t, root, slug, "", map[string]string{"templates/index.html": slug + " index"})
	}
	return root
}

// Retired slugs resolve to the theme that replaced them, for lookups, parent
// chains and activation.
func TestRetiredThemeSlugsResolve(t *testing.T) {
	root := retiredRoot(t)
	writeTheme(t, root, "brand", `{"name":"Brand","parent":"studio-editorial"}`, map[string]string{})
	e := newEnv(t, root)
	ctx := t.Context()
	for old, next := range map[string]string{"minimal": "default", "studio-business": "business", "studio-editorial": "magazine", "studio-portfolio": "portfolio"} {
		info, err := e.svc.Lookup(old)
		if err != nil || info.Slug != next {
			t.Fatalf("Lookup(%q) = %q, %v; want %q", old, info.Slug, err, next)
		}
		if err := e.svc.Activate(ctx, old); err != nil {
			t.Fatal(err)
		}
		if got := e.svc.Active(ctx).Slug; got != next {
			t.Fatalf("activating %q stored %q", old, got)
		}
	}
	chain, err := loadChain(e.svc.roots(), "brand")
	if err != nil || len(chain) != 2 || chain[1].Slug != "magazine" {
		t.Fatalf("brand chain = %+v, %v", chain, err)
	}
	list, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range list {
		if _, retired := retiredThemes[info.Slug]; retired {
			t.Fatalf("retired slug listed: %s", info.Slug)
		}
	}
}

// A site still on a retired theme moves to its replacement once: the active
// theme, that theme's settings and its template overrides follow, without
// replacing anything the new slug already has.
func TestMigrateRetiredActiveTheme(t *testing.T) {
	e := newEnv(t, retiredRoot(t))
	ctx := t.Context()
	e.setting(SettingTheme, "studio-editorial")
	e.setting("theme.studio-editorial.header_style", "framed")
	e.setting("theme.studio-editorial.footer_style", "columns")
	e.setting("theme.magazine.footer_style", "minimal")
	oldDir := filepath.Join(e.cfg.DataDir, "overrides", "studio-editorial", "templates")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "page.html"), []byte("custom page"), 0o644); err != nil {
		t.Fatal(err)
	}

	for range 2 { // idempotent
		if err := e.svc.migrateRetiredTheme(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.app.Deps.Settings.GetString(ctx, SettingTheme, ""); got != "magazine" {
		t.Fatalf("site.theme = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.magazine.header_style", ""); got != "framed" {
		t.Fatalf("header_style = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.magazine.footer_style", ""); got != "minimal" {
		t.Fatalf("existing footer_style replaced: %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(e.cfg.DataDir, "overrides", "magazine", "templates", "page.html"))
	if err != nil || string(raw) != "custom page" {
		t.Fatalf("override not moved: %q %v", raw, err)
	}
}
