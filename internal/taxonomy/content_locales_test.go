package taxonomy

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestPublicTaxonomyCannotClaimConfiguredLanguagePrefix(t *testing.T) {
	app := bootApp(t)
	_, svc := services(t, app)
	ctx := context.Background()
	if err := app.Deps.Settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr"}); err != nil {
		t.Fatal(err)
	}
	tax := Taxonomy{Slug: "fr", Public: true, Name: map[string]string{"en": "French topics"}}
	if err := svc.Save(ctx, tax); err == nil {
		t.Fatal("public taxonomy took French prefix")
	}
	tax.Public = false
	if err := svc.Save(ctx, tax); err != nil {
		t.Fatalf("private taxonomy incorrectly blocked: %v", err)
	}
}
