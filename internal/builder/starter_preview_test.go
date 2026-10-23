package builder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/theme"
)

func TestStarterPreviewUsesUnassignedNavigationAndProposedIdentity(t *testing.T) {
	h, cs, ctx := starterTest(t)
	if err := h.d.Settings.Set(ctx, "site.theme", "default"); err != nil {
		t.Fatal(err)
	}
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "portfolio"))
	if err != nil {
		t.Fatal(err)
	}
	run.Profile.Logo = "proposed-logo"
	if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
		t.Fatal(err)
	}
	options, enabled, err := previewOptions(h.d, ctx, run.Pages["home"])
	if err != nil || !enabled {
		t.Fatalf("preview unavailable: %v", err)
	}
	if options.Theme != "portfolio" || options.Title != run.Title || options.Identity != run.Identity {
		t.Fatalf("wrong proposed identity: %+v", options)
	}
	if options.Logo != run.Profile.Logo {
		t.Fatal("pending logo not supplied to themed preview")
	}
	if options.Menu == nil || len(options.Menu.Items) != 5 {
		t.Fatalf("incomplete preview navigation: %+v", options.Menu)
	}
	for _, entry := range options.Menu.Items {
		if entry.URL == "/" {
			continue
		}
		found := false
		for _, id := range run.Pages {
			item, err := cs.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if entry.URL == "/"+item.Slug {
				found = true
			}
		}
		if !found {
			t.Fatalf("unresolved draft navigation: %s", entry.URL)
		}
	}
	if h.d.Settings.GetString(ctx, "site.theme", "") != "default" {
		t.Fatal("preview changed public theme")
	}
	run.Profile.Logo = ""
	if err := h.d.Settings.Set(ctx, "builder.starter."+run.Token, run); err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err != nil {
		t.Fatal(err)
	}
	if h.d.Settings.GetString(ctx, "site.theme", "") != "portfolio" {
		t.Fatal("activation did not select proposed theme")
	}
}

func TestStarterMissingInstalledThemeCannotPartiallyActivate(t *testing.T) {
	h, cs, ctx := starterTest(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "default"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "default", "theme.json"), []byte(`{"name":"Default","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "site.theme", "default"); err != nil {
		t.Fatal(err)
	}
	if err := theme.New(theme.WithThemesDir(root)).Register(h.d); err != nil {
		t.Fatal(err)
	}
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err == nil {
		t.Fatal("activated missing theme")
	}
	if h.d.Settings.GetString(ctx, "site.theme", "") != "default" {
		t.Fatal("changed theme after failed activation")
	}
	for _, id := range run.Pages {
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "draft" {
			t.Fatalf("partially published page: %+v %v", item, err)
		}
	}
}

func TestLegacyStarterKeepsDefaultTheme(t *testing.T) {
	if runTheme(&starterRun{Kind: "company"}) != "default" {
		t.Fatal("legacy starter was silently redesigned")
	}
}

func TestInstalledCustomThemePreviewWithoutSavedIdentity(t *testing.T) {
	h, _, ctx := starterTest(t)
	t.Setenv(theme.EnvThemesExtra, "../theme/testdata/extra")
	if err := theme.New(theme.WithThemesDir("../../themes")).Register(h.d); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "site.theme", "custom-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Delete(ctx, "builder.identity"); err != nil {
		t.Fatal(err)
	}
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent identity inherits theme", true: "saved identity is preserved"}[saved], func(t *testing.T) {
			expected := brand.Identity{}
			if saved {
				expected = brand.Default()
				expected.Accent = "#174a6c"
				if err := h.d.Settings.Set(ctx, "builder.identity", expected); err != nil {
					t.Fatal(err)
				}
			}
			options, enabled, err := previewOptions(h.d, ctx, "unassigned-page")
			if err != nil || !enabled || options.Theme != "custom-fixture" {
				t.Fatalf("custom active preview missing: %v enabled=%v theme=%q", err, enabled, options.Theme)
			}
			if options.Identity != expected {
				t.Fatalf("preview invented or changed identity: got %+v want %+v", options.Identity, expected)
			}
		})
	}
}
