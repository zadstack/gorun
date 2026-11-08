package content

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestConfiguredPublicLocalesCreateAndLinkWithoutAdminCatalog(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}})
	if err := s.settings.Set(ctx, i18n.ContentLocalesSetting, []string{"fr", "en", "pt-BR"}); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.Set(ctx, "site.locale", "fr"); err != nil {
		t.Fatal(err)
	}
	page, err := s.Create(i18n.WithLocale(ctx, "en"), Input{Type: "page", Title: "Bonjour"})
	if err != nil || page.Locale != "fr" {
		t.Fatalf("admin language became content language: %+v %v", page, err)
	}
	translated, err := s.CreateTranslation(ctx, page.ID, "pt-br")
	if err != nil || translated.Locale != "pt-BR" {
		t.Fatalf("regional translation: %+v %v", translated, err)
	}
	members, err := s.Translations(ctx, page.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("translation group: %+v %v", members, err)
	}
	reserved, err := s.Create(ctx, Input{Type: "page", Title: "Reserved", Slug: "pt-br", Locale: "fr"})
	if err != nil || reserved.Slug == "pt-br" {
		t.Fatalf("locale prefix collision: %+v %v", reserved, err)
	}
}

func TestContentSaveRechecksLanguagesAndRouteReservationInsideTransaction(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}})
	if err := s.settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.save(ctx, nil, Input{Type: "page", Title: "Must not save", Locale: "fr"}, saveOpts{inTx: func(tx *sql.Tx) error {
		return s.settings.SetTx(ctx, tx, i18n.ContentLocalesSetting, []string{"ar", "en"})
	}})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Fields["locale"] == "" {
		t.Fatalf("stale locale was committed: %v", err)
	}
	if err := s.settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.save(ctx, nil, Input{Type: "page", Title: "Reserved after validation", Slug: "fr", Locale: "ar"}, saveOpts{inTx: func(tx *sql.Tx) error {
		return s.settings.SetTx(ctx, tx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr"})
	}})
	if err != nil || page.Slug == "fr" {
		t.Fatalf("stale prefix reservation: %+v %v", page, err)
	}
}

func TestContentMutationDoesNotTreatMalformedLanguageSettingsAsMissing(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "page", Public: true, Name: map[string]string{"en": "Page"}})
	if err := s.settings.Set(ctx, i18n.ContentLocalesSetting, []string{"fr", "fr"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Input{Type: "page", Title: "Must not save under fallback", Locale: "ar"}); err == nil {
		t.Fatal("malformed configuration silently used legacy language defaults")
	}
}

func TestPublicTypesAndArchivesCannotClaimConfiguredLanguagePrefixes(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr", "pt-BR"}); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []Type{
		{Slug: "fr", Public: true, Name: map[string]string{"en": "French route"}},
		{Slug: "guides", Public: true, HasArchive: true, ArchiveSlug: "pt-br", Name: map[string]string{"en": "Guides"}},
	} {
		if err := s.SaveType(ctx, typ); err == nil {
			t.Fatalf("public route took language prefix: %+v", typ)
		}
	}
	if err := s.SaveType(ctx, Type{Slug: "fr", Public: false, Name: map[string]string{"en": "Internal record"}}); err != nil {
		t.Fatalf("private type incorrectly blocked: %v", err)
	}
}
