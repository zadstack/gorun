package wpmigrate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
)

func writeSample(t *testing.T, host string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/wxr/sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "example-press.WordPress.xml")
	if err := os.WriteFile(p, bytes.ReplaceAll(raw, []byte("https://example.org"), []byte(host)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestImportWXRFile is the `gorun import-wxr` entry point: synchronous,
// whole pipeline, menus from nav_menu_item, validation report.
func TestImportWXRFile(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	res, err := ImportWXRFile(e.ctx(), e.app.Deps, writeSample(t, e.wp.URL()), Options{Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusValidated || res.Counts["content"][ItemDone] != 3 || res.Counts["menu"][ItemDone] != 1 || res.Report == nil {
		t.Fatalf("result = %+v", res)
	}
	if n := e.svc.wxr.parses.Load(); n != 1 {
		t.Fatalf("the export was parsed %d times, want 1", n)
	}
	if n := e.svc.wxr.len(); n != 0 {
		t.Fatalf("finished run left %d cached exports", n)
	}
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	menu, err := ms.Get(e.ctx(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if len(menu.Items) != 1 || menu.Items[0].Kind != "content" || menu.Items[0].Label != "بني" ||
		len(menu.Items[0].Children) != 1 || menu.Items[0].Children[0].URL != "https://example.net/examplepress" {
		t.Fatalf("menu = %+v", menu.Items)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE email = 'editor@example.org' AND role = 'author'`); n != 1 {
		t.Fatalf("author not imported")
	}
	if _, err := ImportWXRFile(e.ctx(), e.app.Deps, filepath.Join(t.TempDir(), "missing.xml"), Options{}); err == nil {
		t.Fatal("missing file must fail")
	}
}

// TestSlugConflictPolicies covers skip, overwrite and suffix against a page
// that existed before the import.
func TestSlugConflictPolicies(t *testing.T) {
	for _, policy := range []string{SlugSkip, SlugOverwrite, SlugSuffix} {
		t.Run(policy, func(t *testing.T) {
			e := newEnv(t)
			cs := e.contentSvc()
			existing, err := cs.Create(e.ctx(), content.Input{Type: "page", Slug: "platforms", Title: "Local page", Status: "published"})
			if err != nil {
				t.Fatal(err)
			}
			id := e.wxrMigration(Options{SlugPolicy: policy})
			if st := e.run(id); st.Status != StatusValidated {
				t.Fatalf("status %s %+v", st.Status, e.failedItems(id))
			}
			it := e.item(id, "content", "page", "1107")
			local, _ := cs.Get(e.ctx(), existing.ID)
			rd, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
			switch policy {
			case SlugSkip:
				if it.Status != ItemSkipped || it.Error != "slug_conflict" || local.Title != "Local page" {
					t.Fatalf("skip: %+v / %s", it, local.Title)
				}
				child := e.item(id, "content", "page", "1108")
				if child.Status != ItemDone {
					t.Fatalf("child of a skipped page must still import: %+v", child)
				}
			case SlugOverwrite:
				if it.Status != ItemDone || it.LocalID != existing.ID || local.Title != "بني" {
					t.Fatalf("overwrite: %+v / %s", it, local.Title)
				}
			case SlugSuffix:
				if it.Status != ItemDone || it.LocalURL != "/platforms-2" || local.Title != "Local page" {
					t.Fatalf("suffix: %+v / %s", it, local.Title)
				}
				if r, ok := rd.Lookup(e.ctx(), "/platforms"); ok {
					t.Fatalf("must not redirect away from the existing local page: %+v", r)
				}
			}
		})
	}
}

// TestMenuSlugCollisionKeepsLocalMenu: a local "primary" menu is not
// replaced; the import creates primary-imported, and a re-run reuses it.
func TestMenuSlugCollisionKeepsLocalMenu(t *testing.T) {
	e := newEnv(t)
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	local, err := ms.Save(e.ctx(), menus.Menu{Slug: "primary", Name: "Local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.SaveItems(e.ctx(), local.ID, []menus.Item{{Label: "Home", Kind: "custom", URL: "/"}}); err != nil {
		t.Fatal(err)
	}
	id := e.wxrMigration(Options{})
	e.run(id)
	got, _ := ms.Get(e.ctx(), "primary")
	if len(got.Items) != 1 || got.Items[0].Label != "Home" {
		t.Fatalf("local menu replaced: %+v", got.Items)
	}
	imp, err := ms.Get(e.ctx(), "primary-imported")
	if err != nil || len(imp.Items) != 1 {
		t.Fatalf("imported menu = %+v %v", imp, err)
	}
	e.run(id)
	if n := e.count(`SELECT count(*) FROM menus`); n != 2 {
		t.Fatalf("re-run created another menu: %d", n)
	}
}
