package menus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestMenusAPILocaleLocationsCoexist(t *testing.T) {
	s := boot(t, "admin")
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	created := make(map[string]string)
	for _, locale := range []string{"en", "ar"} {
		body := `{"name":"Main ` + locale + `","slug":"main-` + locale + `","location":"primary","locale":"` + locale + `","items":[{"label":"Home","url":"/"}]}`
		resp, out := s.do("POST", "/api/v1/menus", body, "application/json")
		var env struct {
			Data menus.Menu `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || resp.StatusCode != http.StatusCreated || env.Data.Locale != locale {
			t.Fatalf("create %s: %d %s", locale, resp.StatusCode, out)
		}
		created[locale] = env.Data.ID
	}
	for locale, id := range created {
		m, err := svc.ByLocationLocale(context.Background(), "primary", locale)
		if err != nil || m.ID != id || m.Locale != locale || len(m.Items) != 1 {
			t.Fatalf("location %s: %+v %v", locale, m, err)
		}
	}
	resp, out := s.do("POST", "/api/v1/menus", `{"id":"`+created["en"]+`","name":"Updated English","slug":"main-en","location":"primary","locale":"en"}`, "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, out)
	}
	for locale, id := range created {
		m, err := svc.ByLocationLocale(context.Background(), "primary", locale)
		if err != nil || m.ID != id || m.Locale != locale || len(m.Items) != 1 {
			t.Fatalf("location after update %s: %+v %v", locale, m, err)
		}
	}
}

func TestMenusAPIRejectsUnsupportedLocale(t *testing.T) {
	s := boot(t, "admin")
	resp, out := s.do("POST", "/api/v1/menus", `{"name":"Invalid locale","location":"primary","locale":"fr"}`, "application/json")
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(out, `"locale"`) {
		t.Fatalf("unsupported locale: %d %s", resp.StatusCode, out)
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	all, err := svc.List(context.Background())
	if err != nil || len(all) != 0 {
		t.Fatalf("invalid menu persisted: %+v %v", all, err)
	}
}
