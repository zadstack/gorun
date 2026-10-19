package content_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// I8 (BUG-01/BUG-05): a public type created at runtime is in Content tabs on
// the next admin request, labelled in the admin user's locale.
func TestAdminNavListsRuntimeTypeInRequestLocale(t *testing.T) {
	s := loggedIn(t, "admin")
	ctx := context.Background()
	us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	u, err := us.GetByEmail(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	setLocale := func(loc string) {
		if _, err := us.Update(ctx, u.ID, auth.UserPatch{Locale: &loc}); err != nil {
			t.Fatal(err)
		}
	}
	setLocale("en")
	if err := s.app.Deps.Settings.Set(ctx, "site.locale", "ar"); err != nil {
		t.Fatal(err)
	}
	nav := func() string {
		_, page := s.do("GET", "/admin/content/post", nil, nil)
		i, j := strings.Index(page, `data-navigation-context="content"`), strings.Index(page, "</main>")
		if i < 0 || j < i {
			t.Fatalf("no contextual navigation:\n%s", page)
		}
		return page[i:j]
	}
	page := nav()
	if strings.Contains(page, `href="/admin/content/case-study"`) {
		t.Fatal("type not created yet")
	}
	if !strings.Contains(page, `href="/admin/content/post"`) {
		t.Fatalf("positive control: the built-in post type must be in the nav:\n%s", page)
	}
	err = contentService(t, s.app).SaveType(ctx, content.Type{Slug: "case-study", Public: true, Supports: []string{"title"},
		Name: map[string]string{"en": "Case Studies", "ar": "دراسات الحالة"}, Plural: map[string]string{"en": "Case Studies", "ar": "دراسات الحالة"}})
	if err != nil {
		t.Fatal(err)
	}
	page = nav()
	if !strings.Contains(page, `href="/admin/content/case-study"`) || !strings.Contains(page, "Case Studies") {
		t.Fatalf("runtime type missing from the en nav:\n%s", page)
	}
	setLocale("ar")
	page = nav()
	if !strings.Contains(page, "دراسات الحالة") {
		t.Fatalf("ar admin must see the Arabic label:\n%s", page)
	}
}
