package cli

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestCommerceOffCatalogExplainsAccountSignInForMembershipAndSaaS(t *testing.T) {
	app := bootData(t, t.TempDir(), `{"commerce":"disabled"}`)
	defer app.Close()
	key := adminKey(t, app, "api")
	status, body := serveStatus(app.Handler, "GET", "/api/v1/system/modules", http.Header{"Authorization": {"Bearer " + key}})
	if status != 200 {
		t.Fatalf("catalog HTTP %d: %s", status, body)
	}
	var envelope struct {
		Data admin.ModuleCatalog `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range envelope.Data.Entries {
		if entry.ID != "membership" && entry.ID != "saas" {
			continue
		}
		found[entry.ID] = true
		if !entry.State.Enabled || !slices.Contains(entry.OptionalDependencies, "commerce") || slices.Contains(entry.Dependencies, "commerce") {
			t.Fatalf("%s activation/dependency: %+v", entry.ID, entry)
		}
		for _, locale := range []string{"ar", "en"} {
			signIn, preserved := "دخول العملاء غير متاح", "محفوظة"
			if locale == "en" {
				signIn, preserved = "customer sign-in is unavailable", "preserved"
			}
			if entry.ID == "saas" {
				preserved = "المسجلين"
				if locale == "en" {
					preserved = "authenticated users"
				}
			}
			matched := false
			for _, message := range entry.Messages {
				text := message.Text[locale]
				if strings.Contains(text, signIn) && strings.Contains(text, preserved) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("%s missing %s Commerce consequence: %+v", entry.ID, locale, entry.Messages)
			}
		}
	}
	if len(found) != 2 {
		t.Fatalf("missing dependent module rows: %v", found)
	}
}

func TestUnavailableAccountPagesUseAnonymousRequestLocale(t *testing.T) {
	app := bootData(t, t.TempDir(), `{"commerce":"disabled"}`)
	defer app.Close()
	for _, locale := range []string{"ar", "en"} {
		direction, title := "rtl", "الدخول غير متاح"
		if locale == "en" {
			direction, title = "ltr", "Sign-in unavailable"
		}
		for _, path := range []string{"/membership", "/saas"} {
			status, body := serveStatus(app.Handler, "GET", path, http.Header{"Accept-Language": {locale}})
			if status != 503 || !strings.Contains(body, `<html lang="`+locale+`" dir="`+direction+`">`) || !strings.Contains(body, `<title>`+title+`</title>`) {
				t.Fatalf("%s locale=%s HTTP %d: %s", path, locale, status, body)
			}
		}
	}
}
