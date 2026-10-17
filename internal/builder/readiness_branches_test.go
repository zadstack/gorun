package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/theme"
)

type fixedReadinessMedia struct {
	media.Service
	items map[string]*media.Media
}

func (f fixedReadinessMedia) Get(_ context.Context, id string) (*media.Media, error) {
	if m, ok := f.items[id]; ok {
		return m, nil
	}
	return nil, media.ErrNotFound
}

func freshReadiness(ctx context.Context, d *kernel.Deps) *readinessBuilder {
	return &readinessBuilder{ctx: ctx, d: d, report: readinessReport{Groups: make([]readinessGroup, 3)}}
}

func TestReadinessReportsUnreadableAndInvalidSettings(t *testing.T) {
	h, _, ctx := starterTest(t)
	for key, value := range map[string]any{"contact.email": 5, "mail.host": 5, "site.base_url": 5, "site.logo": 5, "builder.identity": "not-an-identity", "site.front_page": "missing-front"} {
		if err := h.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	report := buildReadiness(ctx, h.d)
	for id, want := range map[string]string{"contact_email": "unknown", "smtp_config": "unknown", "base_url": "unknown", "logo": "unknown", "identity": "unknown", "front": "fail"} {
		if c := readinessByID(t, report, id); c.Status != want {
			t.Errorf("%s=%s want %s (%s)", id, c.Status, want, c.Evidence)
		}
	}
	invalid := brand.Default()
	invalid.Text = invalid.Background
	for key, value := range map[string]any{"builder.identity": invalid, "site.base_url": "ftp://example.com/path", "contact.email": "Owner <owner@example.com>", "site.logo": "chosen-logo"} {
		if err := h.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	report = buildReadiness(ctx, h.d)
	for id, want := range map[string]string{"identity": "fail", "base_url": "fail", "contact_email": "warn", "logo": "unknown"} {
		if c := readinessByID(t, report, id); c.Status != want {
			t.Errorf("second pass %s=%s want %s", id, c.Status, want)
		}
	}
	if err := h.d.Settings.Set(ctx, "site.base_url", "https://studio.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "contact.email", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	report = buildReadiness(ctx, h.d)
	if c := readinessByID(t, report, "base_url"); c.Status != "pass" {
		t.Fatalf("public https URL: %+v", c)
	}
	if c := readinessByID(t, report, "contact_email"); c.Status != "pass" {
		t.Fatalf("explicit recipient: %+v", c)
	}
	h.d.Services.Set(kernel.ServiceContent, nil)
	report = buildReadiness(ctx, h.d)
	if c := readinessByID(t, report, "front"); c.Status != "unknown" {
		t.Fatalf("missing content service front: %+v", c)
	}
	if c := readinessByID(t, report, "inspection"); c.Status != "unknown" {
		t.Fatalf("missing content service inspection: %+v", c)
	}
}

func TestReadinessLogoChecksRecordAndLocalFile(t *testing.T) {
	h, _, ctx := starterTest(t)
	_, logo := profileMedia(t, h, ctx)
	if err := h.d.Settings.Set(ctx, "site.logo", logo.ID); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "logo"); c.Status != "pass" {
		t.Fatalf("uploaded logo: %+v", c)
	}
	h.d.Services.Set(kernel.ServiceMedia, fixedReadinessMedia{items: map[string]*media.Media{
		"gone-file": {Mime: "image/png", URL: "/media/never-written.png"},
		"remote":    {Mime: "image/png", URL: "https://cdn.example.com/logo.png"},
		"document":  {Mime: "application/pdf", URL: "/media/file.pdf"},
	}})
	for id, want := range map[string]string{"gone-file": "fail", "remote": "unknown", "document": "fail", "absent": "fail"} {
		if err := h.d.Settings.Set(ctx, "site.logo", id); err != nil {
			t.Fatal(err)
		}
		if c := readinessByID(t, buildReadiness(ctx, h.d), "logo"); c.Status != want {
			t.Errorf("logo %s=%s want %s", id, c.Status, want)
		}
	}
}

func TestReadinessHomeDescriptionFallbacks(t *testing.T) {
	h, cs, ctx := starterTest(t)
	if err := h.d.Settings.Set(ctx, "site.title", "Specific studio"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		in   content.Input
		want string
	}{
		"seo meta":   {content.Input{Excerpt: "", SEO: map[string]any{"description": "Meta description"}, Body: ""}, "pass"},
		"body text":  {content.Input{Body: "<p>Visible text fallback</p>"}, "pass"},
		"empty":      {content.Input{Body: "<div></div>"}, "warn"},
		"oversized":  {content.Input{Body: "<p>" + strings.Repeat("a", 256*1024+1) + "</p>"}, "unknown"},
		"blank site": {content.Input{Body: "<p>Text</p>"}, "warn"},
	} {
		t.Run(name, func(t *testing.T) {
			in := test.in
			in.Type, in.Title, in.Status = "page", "Front "+name, "published"
			page, err := cs.Create(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.d.Settings.Set(ctx, "site.front_page", page.ID); err != nil {
				t.Fatal(err)
			}
			title := "Specific studio"
			if name == "blank site" {
				title = "GoRun"
			}
			if err := h.d.Settings.Set(ctx, "site.title", title); err != nil {
				t.Fatal(err)
			}
			c := readinessByID(t, buildReadiness(ctx, h.d), "seo")
			if c.Status != test.want || c.Fix != "/admin/content/page/"+page.ID {
				t.Fatalf("seo %+v want %s", c, test.want)
			}
		})
	}
	if err := h.d.Settings.Set(ctx, "site.title", 7); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "seo"); c.Status != "unknown" {
		t.Fatalf("unreadable title: %+v", c)
	}
}

func TestReadinessImagePathRules(t *testing.T) {
	h, _, ctx := starterTest(t)
	b := freshReadiness(ctx, h.d)
	for raw, want := range map[string]string{
		"http://user@host/x.png":     "fail",
		"/themes/onlyslug":           "fail",
		"/themes/slug/other/x.png":   "fail",
		"/themes/slug/assets/x.png":  "unknown",
		"/uploads/elsewhere.png":     "unknown",
		"/media/":                    "fail",
		"/media/sub\\dir.png":        "fail",
		"%zz":                        "fail",
		"https://cdn.example.com/im": "unknown",
	} {
		if got := b.image(raw); got != want {
			t.Errorf("image(%q)=%s want %s", raw, got, want)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.png"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir.png"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty.png", "dir.png"} {
		if got := localReadinessFile(root, name); got != "fail" {
			t.Errorf("%s=%s want fail", name, got)
		}
	}
	if got := localReadinessFile(filepath.Join(root, "absent"), "x.png"); got != "fail" {
		t.Errorf("missing root=%s", got)
	}
}

type failingThemeLookup struct{}

func (failingThemeLookup) Lookup(string) (theme.Info, error) { return theme.Info{}, theme.ErrNotFound }

func TestReadinessThemeImageLookupFailure(t *testing.T) {
	h, _, ctx := starterTest(t)
	h.d.Services.Set(kernel.ServiceTheme, failingThemeLookup{})
	if got := freshReadiness(ctx, h.d).image("/themes/gone/assets/x.png"); got != "fail" {
		t.Fatalf("missing theme image=%s", got)
	}
}

func TestReadinessInspectContentFeaturedMediaAndContactMarkers(t *testing.T) {
	h, _, ctx := starterTest(t)
	mediaDir := filepath.Join(h.d.Config.DataDir, "media")
	if err := os.MkdirAll(mediaDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "large.png"), []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	h.d.Services.Set(kernel.ServiceMedia, fixedReadinessMedia{items: map[string]*media.Media{
		"no-alt":  {URL: "/media/missing.png", Variants: map[string]media.Variant{"large": {URL: "/media/large.png"}}},
		"broken":  {Alt: "Broken", URL: "/media/missing.png"},
		"remote":  {Alt: "Remote", URL: "https://cdn.example.com/a.png"},
		"healthy": {Alt: "Fine", URL: "/media/large.png"},
	}})
	items := []content.Item{
		{Type: "page", ID: "a", Title: "No alt", FeaturedMediaID: "no-alt", Body: `<section class="hero gp-contact"><img src="https://cdn.example.com/x.png" alt="Remote"></section>`},
		{Type: "page", ID: "b", Title: "Broken", FeaturedMediaID: "broken", Body: `<form action="/forms/contact"></form>`},
		{Type: "page", ID: "c", Title: "Remote", FeaturedMediaID: "remote"},
		{Type: "page", ID: "d", Title: "Gone", FeaturedMediaID: "deleted"},
		{Type: "page", ID: "e", Title: "Healthy", FeaturedMediaID: "healthy"},
		{Type: "page", ID: "f", Title: "Huge", Body: strings.Repeat("a", 256*1024+1)},
	}
	b := freshReadiness(ctx, h.d)
	b.inspectContent(nil, content.Page[content.Item]{Items: items, Total: len(items)})
	if c := readinessByID(t, b.report, "alt"); c.Status != "warn" || !strings.Contains(c.Evidence, "No alt") || c.Fix != "/admin/media" {
		t.Fatalf("featured alt: %+v", c)
	}
	if c := readinessByID(t, b.report, "images"); c.Status != "fail" || !strings.Contains(c.Evidence, ": 2") || !strings.Contains(c.Evidence, "Broken") {
		t.Fatalf("featured images: %+v", c)
	}
	if c := readinessByID(t, b.report, "contact_form"); c.Status != "pass" {
		t.Fatalf("contact markers: %+v", c)
	}
	if c := readinessByID(t, b.report, "bounds"); c.Status != "warn" {
		t.Fatalf("oversized body bounds: %+v", c)
	}
	h.d.Services.Set(kernel.ServiceMedia, nil)
	b = freshReadiness(ctx, h.d)
	b.inspectContent(nil, content.Page[content.Item]{Items: []content.Item{{Type: "page", ID: "x", Title: "Unverified", FeaturedMediaID: "any", Body: "<p>Real</p>"}}, Total: 1})
	if c := readinessByID(t, b.report, "images"); c.Status != "unknown" {
		t.Fatalf("no media service: %+v", c)
	}
	if c := readinessByID(t, b.report, "contact_form"); c.Status != "warn" {
		t.Fatalf("no contact markers: %+v", c)
	}
}

func TestReadinessInspectContentImageCap(t *testing.T) {
	h, _, ctx := starterTest(t)
	body := strings.Repeat(`<img src="https://cdn.example.com/x.png" alt="x">`, 201)
	b := freshReadiness(ctx, h.d)
	b.inspectContent(nil, content.Page[content.Item]{Items: []content.Item{{Type: "page", ID: "x", Title: "Gallery", Body: body}}, Total: 1})
	if c := readinessByID(t, b.report, "bounds"); c.Status != "warn" {
		t.Fatalf("image cap not reported: %+v", c)
	}
	if c := readinessByID(t, b.report, "images"); c.Status != "unknown" {
		t.Fatalf("capped images claimed %s", c.Status)
	}
}

func insertPrimaryMenu(t *testing.T, h *starterHandler, ctx context.Context, items [][3]string) {
	t.Helper()
	ms, _ := kernel.Get[menus.Service](h.d, kernel.ServiceMenus)
	menu, err := ms.Save(ctx, menus.Menu{Slug: "primary-raw", Name: "Primary", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		if _, err := h.d.DB.ExecContext(ctx, `INSERT INTO menu_items(id,menu_id,position,label,kind,ref_id,url) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("i%d", i), menu.ID, i, "Item", it[0], it[1], it[2]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadinessNavigationClassifiesRawLinks(t *testing.T) {
	for name, test := range map[string]struct {
		items [][3]string
		want  string
	}{
		"empty":          {nil, "warn"},
		"credential url": {[][3]string{{"custom", "", "http://user:pw@example.com/"}}, "fail"},
		"external":       {[][3]string{{"custom", "", "https://example.com/"}}, "unknown"},
		"unknown route":  {[][3]string{{"custom", "", "/not-a-page"}}, "unknown"},
		"term":           {[][3]string{{"term", "tag", ""}}, "unknown"},
		"missing ref":    {[][3]string{{"content", "vanished", ""}}, "fail"},
		"fragment only":  {[][3]string{{"custom", "", "#top"}}, "pass"},
	} {
		t.Run(name, func(t *testing.T) {
			h, _, ctx := starterTest(t)
			insertPrimaryMenu(t, h, ctx, test.items)
			if c := readinessByID(t, buildReadiness(ctx, h.d), "navigation"); c.Status != test.want {
				t.Fatalf("navigation=%s want %s: %s", c.Status, test.want, c.Evidence)
			}
		})
	}
}

func TestReadinessNavigationOverCapAndBrokenItemsTable(t *testing.T) {
	h, _, ctx := starterTest(t)
	items := make([][3]string, 201)
	for i := range items {
		items[i] = [3]string{"custom", "", "#top"}
	}
	insertPrimaryMenu(t, h, ctx, items)
	if c := readinessByID(t, buildReadiness(ctx, h.d), "navigation"); c.Status != "unknown" || !strings.Contains(c.Evidence, "200") {
		t.Fatalf("over cap navigation: %+v", c)
	}
	if _, err := h.d.DB.ExecContext(ctx, `ALTER TABLE menu_items RENAME TO menu_items_gone`); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "navigation"); c.Status != "unknown" {
		t.Fatalf("broken items table: %+v", c)
	}
}

func TestReadinessBackupDirectoryEdgeCases(t *testing.T) {
	h, _, ctx := starterTest(t)
	dir := filepath.Join(h.d.Config.DataDir, "backups")
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "backup"); c.Status != "unknown" {
		t.Fatalf("backup path is a file: %+v", c)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes.txt", "20261012T120000Z.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "backup"); c.Status != "warn" {
		t.Fatalf("incomplete pair: %+v", c)
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("extra-%03d", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "backup"); c.Status != "unknown" || !strings.Contains(c.Evidence, "200") {
		t.Fatalf("over cap backups: %+v", c)
	}
}

func TestReadinessActiveThemeMetadata(t *testing.T) {
	h, _, ctx := starterTest(t)
	if c := readinessByID(t, buildReadiness(ctx, h.d), "active_theme"); c.Status != "unknown" {
		t.Fatalf("no theme service: %+v", c)
	}
	t.Setenv(theme.EnvThemesExtra, "../theme/testdata/extra")
	if err := theme.New(theme.WithThemesDir("../../themes")).Register(h.d); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "site.theme", "custom-fixture"); err != nil {
		t.Fatal(err)
	}
	if c := readinessByID(t, buildReadiness(ctx, h.d), "active_theme"); c.Status != "pass" || !strings.Contains(c.Evidence, "custom-fixture") {
		t.Fatalf("installed theme: %+v", c)
	}
}
