package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/theme"
)

type unavailableReadinessMedia struct{ media.Service }

func (unavailableReadinessMedia) Get(context.Context, string) (*media.Media, error) {
	return nil, errors.New("media query unavailable")
}

func TestReadinessMediaQueryErrorIsUnverified(t *testing.T) {
	h, _, ctx := starterTest(t)
	h.d.Services.Set(kernel.ServiceMedia, unavailableReadinessMedia{})
	if err := h.d.Settings.Set(ctx, "site.logo", "chosen-logo"); err != nil {
		t.Fatal(err)
	}
	if got := readinessByID(t, buildReadiness(ctx, h.d), "logo"); got.Status != "unknown" {
		t.Fatalf("query error claimed missing image: %+v", got)
	}
}

func readinessByID(t *testing.T, r readinessReport, id string) readinessCheck {
	t.Helper()
	for _, g := range r.Groups {
		for _, c := range g.Checks {
			if c.ID == id {
				return c
			}
		}
	}
	t.Fatalf("missing %s", id)
	return readinessCheck{}
}
func TestReadinessInspectsPublishedSiteWithoutMutating(t *testing.T) {
	h, cs, ctx := starterTest(t)
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Home", Slug: "ready", Status: "published", Body: `<h1>Real studio</h1><p>Replace this example before launch.</p><img src="/media/missing.svg">`})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.d.Settings.Set(ctx, "site.front_page", page.ID); err != nil {
		t.Fatal(err)
	}
	if err = h.d.Settings.Set(ctx, "builder.identity", brand.Default()); err != nil {
		t.Fatal(err)
	}
	before := content.EditToken(page)
	r := buildReadiness(ctx, h.d)
	for id, status := range map[string]string{"front": "pass", "identity": "pass", "placeholders": "warn", "alt": "warn", "images": "fail", "smtp_delivery": "unknown", "backup": "warn"} {
		if c := readinessByID(t, r, id); c.Status != status {
			t.Errorf("%s=%s want %s", id, c.Status, status)
		}
	}
	after, err := cs.Get(ctx, page.ID)
	if err != nil || content.EditToken(after) != before {
		t.Fatal("readiness mutated content")
	}
	if strings.Contains(readinessByID(t, r, "smtp_delivery").Evidence, "password") {
		t.Fatal("secret exposed")
	}
}
func TestReadinessLocalFilesCannotEscapeOrFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("sensitive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret", "escape/secret", "/etc/passwd"} {
		if localReadinessFile(root, path) == "pass" {
			t.Errorf("escaped via %s", path)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "image.svg"), []byte("svg"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := localReadinessFile(root, "image.svg"); got != "pass" {
		t.Fatal(got)
	}
}

type readinessThemeLookup struct{ dir string }

func (f readinessThemeLookup) Lookup(slug string) (theme.Info, error) {
	return theme.Info{Slug: slug, Dir: f.dir}, nil
}
func TestReadinessThemeImagesAndRawPrivateNavigation(t *testing.T) {
	h, cs, ctx := starterTest(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "cover.svg"), []byte("svg"), 0600); err != nil {
		t.Fatal(err)
	}
	h.d.Services.Set(kernel.ServiceTheme, readinessThemeLookup{root})
	b := readinessBuilder{ctx: ctx, d: h.d}
	if got := b.image("/themes/portfolio/assets/cover.svg"); got != "pass" {
		t.Fatalf("theme image: %s", got)
	}
	if got := b.image("https://localhost:1/private"); got != "unknown" {
		t.Fatalf("external image attempted/claimed: %s", got)
	}
	draft, err := cs.Create(ctx, content.Input{Type: "page", Title: "Hidden", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[menus.Service](h.d, kernel.ServiceMenus)
	if _, err = ms.Save(ctx, menus.Menu{Slug: "raw-primary", Name: "Raw", Location: "primary", Items: []menus.Item{{Label: "Hidden", Kind: menus.KindContent, RefID: draft.ID}}}); err != nil {
		t.Fatal(err)
	}
	report := buildReadiness(ctx, h.d)
	if c := readinessByID(t, report, "navigation"); c.Status != "fail" {
		t.Fatalf("resolved menu hid broken link: %+v", c)
	}
	if _, err = h.d.DB.ExecContext(ctx, "ALTER TABLE menus RENAME TO menus_unavailable"); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "navigation"); c.Status != "unknown" {
		t.Fatalf("schema failure claimed healthy: %+v", c)
	}
}
func TestReadinessBackupsConfiguredSMTPAndLocalization(t *testing.T) {
	h, _, ctx := starterTest(t)
	if err := h.d.Settings.Set(ctx, "mail.host", "smtp.private.example"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "mail.password", "never-expose-secret"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.d.Config.DataDir, "backups")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261012T120000Z.db", "20261012T120000Z.tar.gz"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("metadata-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, locale := range []string{"ar", "en"} {
		report := buildReadiness(i18n.WithLocale(ctx, locale), h.d)
		if c := readinessByID(t, report, "backup"); c.Status != "pass" {
			t.Fatal(c)
		}
		if c := readinessByID(t, report, "smtp_config"); c.Status != "pass" {
			t.Fatal(c)
		}
		if c := readinessByID(t, report, "smtp_delivery"); c.Status != "unknown" {
			t.Fatal(c)
		}
		raw := fmt.Sprint(report)
		if strings.Contains(raw, "never-expose-secret") || strings.Contains(raw, "smtp.private.example") {
			t.Fatal("SMTP secret/host disclosed")
		}
		if locale == "ar" && !strings.Contains(report.Note, "فحص محلي") {
			t.Fatal("Arabic missing")
		}
	}
}

func TestReadinessPrivateHomeAndPartialScopeNeverClaimPass(t *testing.T) {
	h, cs, ctx := starterTest(t)
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Private front", Status: "published", Body: "<p>Confidential copy</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.d.Settings.Set(ctx, "site.front_page", page.ID); err != nil {
		t.Fatal(err)
	}
	if err = h.d.Settings.Set(ctx, "site.title", "Specific studio"); err != nil {
		t.Fatal(err)
	}
	if _, err = h.d.DB.ExecContext(ctx, "UPDATE content_types SET public=0 WHERE slug='page'"); err != nil {
		t.Fatal(err)
	}
	report := buildReadiness(ctx, h.d)
	if c := readinessByID(t, report, "front"); c.Status != "fail" {
		t.Fatal(c)
	}
	if c := readinessByID(t, report, "seo"); c.Status == "pass" {
		t.Fatal("private home metadata accepted")
	}
	b := readinessBuilder{ctx: ctx, d: h.d, report: readinessReport{Groups: make([]readinessGroup, 3)}}
	b.inspectContent(cs, content.Page[content.Item]{Items: []content.Item{{Type: "page", ID: "observed", Title: "Real page", Body: "<h1>Real page</h1>"}}, Total: 101})
	for _, id := range []string{"placeholders", "alt", "images"} {
		if c := readinessByID(t, b.report, id); c.Status != "unknown" {
			t.Fatalf("partial %s claimed %s", id, c.Status)
		}
	}
}
func TestReadinessFindingsLinkToTheOffendingBuilder(t *testing.T) {
	h, cs, ctx := starterTest(t)
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Needs editing", Slug: "needs-edit", Status: "published", Template: "builder", Body: `<p>Replace this example</p><img src="/media/missing.png">`})
	if err != nil {
		t.Fatal(err)
	}
	report := buildReadiness(ctx, h.d)
	for _, id := range []string{"placeholders", "alt", "images"} {
		c := readinessByID(t, report, id)
		if c.Fix != "/admin/builder/"+page.ID || !strings.Contains(c.Evidence, page.Title) {
			t.Fatalf("finding not actionable: %+v", c)
		}
	}
	ms, _ := kernel.Get[menus.Service](h.d, kernel.ServiceMenus)
	if _, err = ms.Save(ctx, menus.Menu{Slug: "default-ar", Name: "Arabic menu", Location: "primary", Locale: "ar", Items: []menus.Item{{Label: "Home", Kind: menus.KindContent, RefID: page.ID}}}); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "navigation"); c.Status != "pass" {
		t.Fatalf("default ar menu missing: %+v", c)
	}
}
