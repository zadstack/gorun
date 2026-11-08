package site_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestConfiguredLanguagesRouteAfterBootAndKeepRegionalTranslations(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	// Configuration changes after the router has been built must take effect.
	if err := e.app.Deps.Settings.Set(ctx, i18n.ContentLocalesSetting, []string{"ar", "en", "fr", "pt-BR"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	fr := publish(t, cs, content.Input{Type: "post", Slug: "bonjour", Title: "Bonjour le monde", Locale: "fr"})
	pt, err := cs.CreateTranslation(ctx, fr.ID, "pt-br")
	if err != nil {
		t.Fatal(err)
	}
	input := content.InputFromItem(pt)
	input.Title, input.Status = "Ola mundo", "published"
	if _, err := cs.Update(ctx, pt.ID, input); err != nil {
		t.Fatal(err)
	}
	menuService, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	if _, err := menuService.Save(ctx, menus.Menu{Name: "French", Slug: "french", Locale: "fr", Location: "primary", Items: []menus.Item{{Kind: menus.KindCustom, Label: "Navigation francaise", URL: "/fr/"}}}); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ path, locale, title string }{{"/fr/post/bonjour", "fr", "Bonjour le monde"}, {"/pt-BR/post/bonjour", "pt-BR", "Ola mundo"}} {
		response, body := e.get(scenario.path)
		if response.StatusCode != 200 {
			t.Fatalf("%s: %d %s", scenario.path, response.StatusCode, body)
		}
		mustContain(t, body, `lang="`+scenario.locale+`"`, scenario.title, `hreflang="fr"`, `hreflang="pt-BR"`)
		if scenario.locale == "fr" {
			mustContain(t, body, "Navigation francaise")
		}
	}
	response, _ := e.get("/pt-br/post/bonjour?q=keep")
	if response.StatusCode != 301 || response.Header.Get("Location") != "/pt-BR/post/bonjour?q=keep" {
		t.Fatalf("canonical regional tag: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	_, sitemap := e.get("/sitemap.xml")
	mustContain(t, sitemap, "/fr/post/bonjour", "/pt-BR/post/bonjour")
	if err := e.app.Deps.Settings.Set(ctx, "site.locale", "fr"); err != nil {
		t.Fatal(err)
	}
	response, body := e.get("/post/bonjour")
	if response.StatusCode != 200 || !strings.Contains(body, `lang="fr"`) {
		t.Fatalf("default French: %d %s", response.StatusCode, body)
	}
	response, _ = e.get("/fr/post/bonjour?x=1")
	if response.StatusCode != 301 || response.Header.Get("Location") != "/post/bonjour?x=1" {
		t.Fatalf("default prefix redirect: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
}
