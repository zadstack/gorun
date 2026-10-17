package theme

import (
	"fmt"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredRetiredThemePreservesOverrides(t *testing.T) {
	data := t.TempDir()
	p := filepath.Join(data, "overrides", "studio-business", "templates")
	os.MkdirAll(p, 0755)
	os.WriteFile(filepath.Join(p, "index.html"), []byte("CUSTOM EXISTING IDENTITY"), 0644)
	e := newEnv(t, retiredRoot(t), func(c *config.Config) { c.DataDir = data; c.Theme = "studio-business" })
	got, err := e.svc.ReadTemplate(t.Context(), "index.html")
	if err != nil || got != "CUSTOM EXISTING IDENTITY" {
		t.Fatalf("existing custom template lost: got=%q err=%v", got, err)
	}
}
func TestMigrationPreservesNonConflictingOverrides(t *testing.T) {
	e := newEnv(t, retiredRoot(t))
	e.setting(SettingTheme, "studio-business")
	for slug, name := range map[string]string{"studio-business": "page.html", "business": "index.html"} {
		p := filepath.Join(e.cfg.DataDir, "overrides", slug, "templates")
		os.MkdirAll(p, 0755)
		os.WriteFile(filepath.Join(p, name), []byte("CUSTOM "+name), 0644)
	}
	if err := e.svc.migrateRetiredTheme(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.ReadTemplate(t.Context(), "page.html")
	if err != nil || got != "CUSTOM page.html" {
		t.Fatalf("non-conflicting published override lost: got=%q err=%v", got, err)
	}
}

func TestMigrationConflictLeavesSettingsAndFilesUntouched(t *testing.T) {
	e := newEnv(t, retiredRoot(t))
	e.setting(SettingTheme, "studio-business")
	e.setting("theme.studio-business.header_style", "framed")
	for slug, value := range map[string]string{"studio-business": "old identity", "business": "new identity"} {
		p := filepath.Join(e.cfg.DataDir, "overrides", slug, "_drafts", "templates")
		os.MkdirAll(p, 0755)
		os.WriteFile(filepath.Join(p, "index.html"), []byte(value), 0644)
	}
	err := e.svc.migrateRetiredTheme(t.Context())
	if err == nil {
		t.Fatal("conflicting drafts must fail migration")
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), SettingTheme, ""); got != "studio-business" {
		t.Fatalf("active theme changed: %q", got)
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), "theme.business.header_style", ""); got != "" {
		t.Fatalf("settings changed on conflict: %q", got)
	}
	for slug, value := range map[string]string{"studio-business": "old identity", "business": "new identity"} {
		got, err := os.ReadFile(filepath.Join(e.cfg.DataDir, "overrides", slug, "_drafts", "templates", "index.html"))
		if err != nil || string(got) != value {
			t.Fatalf("override changed: %q %v", got, err)
		}
	}
}

func TestMigrationSettingsFailureRollsBackAndRetries(t *testing.T) {
	e := newEnv(t, retiredRoot(t))
	e.setting(SettingTheme, "studio-business")
	e.setting("theme.studio-business.header_style", "framed")
	p := filepath.Join(e.cfg.DataDir, "overrides", "studio-business", "templates")
	os.MkdirAll(p, 0755)
	os.WriteFile(filepath.Join(p, "index.html"), []byte("original"), 0644)
	_, err := e.app.Deps.DB.Exec(`CREATE TRIGGER fail_theme BEFORE UPDATE ON settings WHEN NEW.key='site.theme' BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.svc.migrateRetiredTheme(t.Context()); err == nil {
		t.Fatal("expected settings failure")
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), SettingTheme, ""); got != "studio-business" {
		t.Fatalf("active theme changed: %q", got)
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), "theme.business.header_style", ""); got != "" {
		t.Fatalf("partial settings survived rollback: %q", got)
	}
	if raw, err := os.ReadFile(filepath.Join(p, "index.html")); err != nil || string(raw) != "original" {
		t.Fatalf("source lost: %q %v", raw, err)
	}
	if _, err = e.app.Deps.DB.Exec(`DROP TRIGGER fail_theme`); err != nil {
		t.Fatal(err)
	}
	if err = e.svc.migrateRetiredTheme(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.ReadTemplate(t.Context(), "index.html")
	if err != nil || got != "original" {
		t.Fatalf("retry did not preserve identity: %q %v", got, err)
	}
}

func TestMigrationRejectsSymlinkOverrides(t *testing.T) {
	e := newEnv(t, retiredRoot(t))
	e.setting(SettingTheme, "studio-business")
	source := filepath.Join(e.cfg.DataDir, "overrides", "studio-business")
	os.MkdirAll(filepath.Dir(source), 0755)
	if err := os.Symlink(t.TempDir(), source); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.migrateRetiredTheme(t.Context()); err == nil {
		t.Fatal("symlink override must be rejected")
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), SettingTheme, ""); got != "studio-business" {
		t.Fatalf("active theme changed: %q", got)
	}
}

func TestInstalledLegacyParentPreservesBrandIdentity(t *testing.T) {
	for _, withReplacement := range []bool{false, true} {
		t.Run(fmt.Sprint(withReplacement), func(t *testing.T) {
			root, extra := t.TempDir(), t.TempDir()
			writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "default"})
			if withReplacement {
				writeTheme(t, root, "magazine", "", map[string]string{"templates/index.html": "REPLACEMENT"})
			}
			writeTheme(t, extra, "studio-editorial", "", map[string]string{"templates/index.html": `LEGACY {{setting "theme.studio-editorial.masthead_note"}}`})
			writeTheme(t, extra, "brand", `{"name":"Brand","parent":"studio-editorial"}`, map[string]string{})
			t.Setenv(EnvThemesExtra, extra)
			e := newEnv(t, root, func(c *config.Config) { c.Theme = "brand" })
			e.setting("theme.studio-editorial.masthead_note", "Original identity")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil || !strings.Contains(rec.Body.String(), "LEGACY Original identity") {
				t.Fatalf("legacy rendering changed: %v %s", err, rec.Body.String())
			}

			chain, err := loadChain(e.svc.roots(), "brand")
			if err != nil || len(chain) != 2 || chain[1].Slug != "studio-editorial" {
				t.Fatalf("legacy parent replaced: %+v %v", chain, err)
			}
			got, err := e.svc.ReadTemplate(t.Context(), "index.html")
			if err != nil || !strings.Contains(got, "LEGACY") {
				t.Fatalf("legacy template lost: %q %v", got, err)
			}
			if got := e.app.Deps.Settings.GetString(t.Context(), "theme.studio-editorial.masthead_note", ""); got != "Original identity" {
				t.Fatalf("legacy setting changed: %q", got)
			}
		})
	}
}

func TestInstalledLegacyActiveThemeKeepsSettingsAndOverrides(t *testing.T) {
	root := retiredRoot(t)
	writeTheme(t, root, "studio-business", "", map[string]string{"templates/index.html": "LEGACY"})
	data := t.TempDir()
	p := filepath.Join(data, "overrides", "studio-business", "templates")
	os.MkdirAll(p, 0755)
	os.WriteFile(filepath.Join(p, "index.html"), []byte("CUSTOM LEGACY"), 0644)
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "studio-business"; c.DataDir = data })
	e.setting(SettingTheme, "studio-business")
	e.setting("theme.studio-business.header_style", "framed")
	if err := e.svc.migrateRetiredTheme(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Activate(t.Context(), "studio-business"); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Active(t.Context()).Slug; got != "studio-business" {
		t.Fatalf("legacy theme replaced: %q", got)
	}
	got, err := e.svc.ReadTemplate(t.Context(), "index.html")
	if err != nil || got != "CUSTOM LEGACY" {
		t.Fatalf("legacy override lost: %q %v", got, err)
	}
	if got := e.app.Deps.Settings.GetString(t.Context(), "theme.business.header_style", ""); got != "" {
		t.Fatalf("legacy settings migrated: %q", got)
	}
}
