package taxonomy

import (
	"context"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func fieldErr(t *testing.T, err error, field string) {
	t.Helper()
	var ve *content.ValidationError
	if !errors.As(err, &ve) || ve.Fields[field] == "" {
		t.Fatalf("want a validation error on %q, got %v", field, err)
	}
}

func TestNewTermsTakeTheSiteLocale(t *testing.T) {
	app := bootApp(t)
	cs, ts := services(t, app)
	ctx := context.Background()
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News"})
	if news.Locale != "ar" || news.TranslationGroup != "" {
		t.Fatalf("default locale: %+v", news)
	}
	en := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Sport", Locale: "EN"})
	if en.Locale != "en" {
		t.Fatalf("canonical locale: %q", en.Locale)
	}
	if _, err := ts.SaveTerm(ctx, Term{Taxonomy: "category", Name: "X", Locale: "fr"}); err == nil {
		t.Fatal("a locale outside the content languages was accepted")
	} else {
		fieldErr(t, err, "locale")
	}
	// A tag typed in the editor is created in the item's locale.
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "T", Locale: "en", Terms: map[string][]string{"tag": {"Fresh"}}}); err != nil {
		t.Fatal(err)
	}
	tag, err := ts.GetTermBySlug(ctx, "tag", "fresh")
	if err != nil || tag.Locale != "en" {
		t.Fatalf("auto tag locale: %+v %v", tag, err)
	}
}

func TestTermTranslations(t *testing.T) {
	app := bootApp(t)
	_, ts := services(t, app)
	ctx := context.Background()
	world := mustTerm(t, ts, Term{Taxonomy: "category", Name: "World", Locale: "en"})
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News", Locale: "en", ParentID: world.ID, Description: "Daily"})
	alam, err := ts.CreateTermTranslation(ctx, world.ID, Term{Locale: "ar", Name: "العالم"})
	if err != nil {
		t.Fatal(err)
	}
	if alam.Locale != "ar" || alam.TranslationGroup == "" || alam.Slug != "العالم" || alam.Taxonomy != "category" {
		t.Fatalf("translation: %+v", alam)
	}
	akhbar, err := ts.CreateTermTranslation(ctx, news.ID, Term{Locale: "ar", Name: "أخبار", Slug: "akhbar"})
	if err != nil {
		t.Fatal(err)
	}
	if akhbar.ParentID != alam.ID {
		t.Fatalf("parent maps to its translation: %q want %q", akhbar.ParentID, alam.ID)
	}
	if akhbar.Description != "Daily" || akhbar.Slug != "akhbar" {
		t.Fatalf("copied fields: %+v", akhbar)
	}
	members, err := ts.TermTranslations(ctx, news.ID)
	if err != nil || len(members) != 2 || members[0].Locale != "ar" || members[1].Locale != "en" {
		t.Fatalf("members: %+v %v", members, err)
	}
	// Same locale twice in a group, a locale outside the site, or the source's own locale: refused.
	_, err = ts.CreateTermTranslation(ctx, news.ID, Term{Locale: "ar", Name: "ثاني"})
	fieldErr(t, err, "locale")
	_, err = ts.CreateTermTranslation(ctx, news.ID, Term{Locale: "fr", Name: "Nouvelles"})
	fieldErr(t, err, "locale")
	// Filters.
	list, total, err := ts.Terms(ctx, "category", TermQuery{Locale: "ar"})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("locale filter: %d %+v %v", total, list, err)
	}
	group, _, _ := ts.Terms(ctx, "category", TermQuery{TranslationGroup: akhbar.TranslationGroup})
	if len(group) != 2 {
		t.Fatalf("group filter: %+v", group)
	}
	// A save never moves a term between groups, and the locale cannot collide inside its group.
	en, _ := ts.GetTerm(ctx, news.ID)
	en.TranslationGroup = ""
	en.Locale = "ar"
	_, err = ts.SaveTerm(ctx, *en)
	fieldErr(t, err, "locale")
	en.Locale = "en"
	saved, err := ts.SaveTerm(ctx, *en)
	if err != nil || saved.TranslationGroup != akhbar.TranslationGroup {
		t.Fatalf("group kept on save: %+v %v", saved, err)
	}

	// Unlink dissolves a group of two; link joins them again.
	if err := ts.UnlinkTermTranslation(ctx, akhbar.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.GetTerm(ctx, news.ID); n.TranslationGroup != "" {
		t.Fatalf("group of one left behind: %+v", n)
	}
	if err := ts.LinkTermTranslation(ctx, news.ID, akhbar.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := ts.TermTranslations(ctx, akhbar.ID); len(m) != 2 {
		t.Fatalf("relinked: %+v", m)
	}
	// Linking across taxonomies, to itself or onto a taken locale is refused.
	tag := mustTerm(t, ts, Term{Taxonomy: "tag", Name: "وسم", Locale: "ar"})
	fieldErr(t, ts.LinkTermTranslation(ctx, news.ID, tag.ID), "taxonomy")
	fieldErr(t, ts.LinkTermTranslation(ctx, news.ID, news.ID), "translation")
	other := mustTerm(t, ts, Term{Taxonomy: "category", Name: "سياسة", Locale: "ar"})
	fieldErr(t, ts.LinkTermTranslation(ctx, news.ID, other.ID), "locale")

	// Deleting a member dissolves a group left with one term.
	if err := ts.DeleteTerm(ctx, akhbar.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := ts.GetTerm(ctx, news.ID); n.TranslationGroup != "" {
		t.Fatalf("group survived its last pair: %+v", n)
	}
}

// TestTermLocaleMigrationOnExistingDatabase boots a database whose terms predate the
// translation columns and checks the migration keeps them and gives them the site locale.
func TestTermLocaleMigrationOnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	mods := func() []kernel.Module {
		return []kernel.Module{settingsmod.New(), auth.New(), content.New(), New()}
	}
	app, err := kernel.Boot(context.Background(), cfg, mods())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, ts := services(t, app)
	cat := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Old"})
	if err := app.Deps.Settings.Set(ctx, "site.locale", "en"); err != nil {
		t.Fatal(err)
	}
	// Rewind the schema to before 0003: drop the columns and forget the migration.
	for _, stmt := range []string{
		`DROP INDEX terms_translation_locale`,
		`DROP INDEX terms_locale`,
		`ALTER TABLE terms DROP COLUMN translation_group`,
		`ALTER TABLE terms DROP COLUMN locale`,
		`DELETE FROM schema_migrations WHERE module = 'taxonomy' AND name = '0003_term_translations.sql'`,
	} {
		if _, err := app.Deps.DB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	app.Close()

	app, err = kernel.Boot(ctx, cfg, mods())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_, ts = services(t, app)
	got, err := ts.GetTerm(ctx, cat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Locale != "en" || got.TranslationGroup != "" || got.Name != "Old" {
		t.Fatalf("migrated term: %+v", got)
	}
	var n int
	_ = app.Deps.DB.QueryRowContext(ctx, `SELECT count(*) FROM terms WHERE locale = ''`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d terms without a locale", n)
	}
}
