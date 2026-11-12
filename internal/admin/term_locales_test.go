package admin_test

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// A content language used by a taxonomy term cannot be removed, like one used by content.
func TestSettingsRefuseRemovingALanguageUsedByTerms(t *testing.T) {
	s := boot(t, content.New(), taxonomy.New())
	user := s.createUser("admin@example.com", "admin", "en")
	ctx := context.Background()
	if err := s.app.Deps.Settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr"}); err != nil {
		t.Fatal(err)
	}
	ts, _ := kernel.Get[taxonomy.Service](s.app.Deps, kernel.ServiceTaxonomy)
	if _, err := ts.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Name: "Actualités", Locale: "fr"}); err != nil {
		t.Fatal(err)
	}
	status, out := apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.title":"Languages","site.content_locales":["ar","en"]}`)
	if status != 422 {
		t.Fatalf("removed a term language: %d %v", status, out)
	}
	if status, out = apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.title":"Languages","site.content_locales":["ar","fr"]}`); status != 200 {
		t.Fatalf("removing an unused language: %d %v", status, out)
	}
}
