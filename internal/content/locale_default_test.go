package content

import (
	"context"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

// New content without an explicit locale follows the request/admin chrome
// locale when present, not site.locale — the soak bug where English drafts
// were stored as ar on an Arabic-default site.
func TestCreateDefaultsLocaleToRequestNotSite(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "post", Name: map[string]string{"en": "Post"}, Supports: []string{"title"}})

	if err := s.settings.Set(ctx, "site.locale", "ar"); err != nil {
		t.Fatal(err)
	}
	en, err := s.Create(i18n.WithLocale(ctx, "en"), Input{Type: "post", Title: "Soak EN Post"})
	if err != nil {
		t.Fatal(err)
	}
	if en.Locale != "en" {
		t.Fatalf("EN request locale with site.locale=ar: got %q", en.Locale)
	}

	if err := s.settings.Set(ctx, "site.locale", "en"); err != nil {
		t.Fatal(err)
	}
	ar, err := s.Create(i18n.WithLocale(ctx, "ar"), Input{Type: "post", Title: "مقال اختبار"})
	if err != nil {
		t.Fatal(err)
	}
	if ar.Locale != "ar" {
		t.Fatalf("AR request locale with site.locale=en: got %q", ar.Locale)
	}

	// No request locale (API key / system): keep site.locale.
	if err := s.settings.Set(ctx, "site.locale", "en"); err != nil {
		t.Fatal(err)
	}
	sys, err := s.Create(ctx, Input{Type: "post", Title: "API without locale"})
	if err != nil {
		t.Fatal(err)
	}
	if sys.Locale != "en" {
		t.Fatalf("no request locale should use site.locale: got %q", sys.Locale)
	}
}

func TestCreateRejectsUnsupportedLocale(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "post", Name: map[string]string{"en": "Post"}, Supports: []string{"title"}})
	_, err := s.Create(context.Background(), Input{Type: "post", Title: "x", Locale: "fr"})
	var ve *ValidationError
	if err == nil || !errors.As(err, &ve) || ve.Fields["locale"] != "content.validation.locale" {
		t.Fatalf("want locale validation, got %v", err)
	}
}
