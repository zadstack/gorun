package admin_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestSettingsConfigureContentLanguagesWithoutChangingAdminLocale(t *testing.T) {
	s := boot(t, content.New(), menus.New())
	user := s.createUser("admin@example.com", "admin", "en")
	status, out := apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.title":"Languages","site.content_locales":["ar","en","fr","pt-br"],"site.locale":"fr"}`)
	if status != 200 {
		t.Fatalf("configure: %d %v", status, out)
	}
	data := out["data"].(map[string]any)
	if !reflect.DeepEqual(data["site.content_locales"], []any{"ar", "en", "fr", "pt-BR"}) || data["site.locale"] != "fr" {
		t.Fatalf("locales: %v", data)
	}
	current, err := s.users().Get(context.Background(), user.ID)
	if err != nil || current.Locale != "en" {
		t.Fatalf("admin locale changed: %+v %v", current, err)
	}
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	if _, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "Bonjour", Locale: "fr"}); err != nil {
		t.Fatal(err)
	}
	status, _ = apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.content_locales":["ar","en"],"site.locale":"en","site.title":"Must roll back"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("removed used content locale: %d", status)
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "site.title", ""); got != "Languages" {
		t.Fatalf("partial update: %s", got)
	}
}

func TestSettingsLanguageConfigurationRejectsCollidingRoutesAndMenus(t *testing.T) {
	s := boot(t, content.New(), menus.New())
	user := s.createUser("admin@example.com", "admin", "en")
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	if _, err := cs.Create(ctx, content.Input{Type: "page", Title: "Existing French path", Slug: "fr"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"site.title":"Languages","site.content_locales":["ar","en","fr"]}`,
		`{"site.title":"Languages","site.content_locales":["ar","en","api"]}`,
		`{"site.title":"Languages","site.content_locales":["ar","en","pt-br","pt-BR"]}`,
		`{"site.title":"Languages","site.content_locales":[]}`,
		`{"site.title":"Languages","site.content_locales":["ar,en"]}`,
	} {
		status, out := apiCall(t, s, user, "PATCH", "/api/v1/settings", body)
		if status != 422 {
			t.Fatalf("invalid languages accepted: %s: %d %v", body, status, out)
		}
	}
	if err := s.app.Deps.Settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "de"}); err != nil {
		t.Fatal(err)
	}
	menu, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	if _, err := menu.Save(ctx, menus.Menu{Name: "German menu", Slug: "german", Locale: "de"}); err != nil {
		t.Fatal(err)
	}
	status, out := apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.title":"Languages","site.content_locales":["ar","en"]}`)
	if status != 422 {
		t.Fatalf("removed menu language: %d %v", status, out)
	}
}

func TestSettingsPatchKeepsConfiguredDefaultWhenSiteLocaleWasNeverStored(t *testing.T) {
	s := boot(t)
	user := s.createUser("admin@example.com", "admin", "en")
	if err := s.app.Deps.Settings.Set(context.Background(), i18n.ContentLocalesSetting, []string{"fr", "en"}); err != nil {
		t.Fatal(err)
	}
	status, out := apiCall(t, s, user, "PATCH", "/api/v1/settings", `{"site.title":"French defaults"}`)
	if status != 200 {
		t.Fatalf("unrelated patch rejected implicit French default: %d %v", status, out)
	}
	if out["data"].(map[string]any)["site.locale"] != "fr" {
		t.Fatalf("default changed to admin language: %v", out)
	}
}
