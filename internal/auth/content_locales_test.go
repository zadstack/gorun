package auth_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestSetupPreservesConfiguredContentLanguageAndCreatesEnglishAdmin(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	if err := s.app.Deps.Settings.Set(ctx, i18n.ContentLocalesSetting, []string{"fr"}); err != nil {
		t.Fatal(err)
	}
	if err := s.app.Deps.Settings.Set(ctx, "site.locale", "fr"); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/setup", nil)
	token := csrfRe.FindStringSubmatch(page)
	if len(token) < 2 {
		t.Fatal("setup csrf missing")
	}
	response, body := s.do("POST", "/admin/setup", url.Values{"_csrf": {token[1]}, "name": {"Admin"}, "email": {"configured-admin@example.test"}, "password": {"correct horse battery"}, "title": {"French site"}, "locale": {"en"}})
	if response.StatusCode != 303 {
		t.Fatalf("setup: %d %s", response.StatusCode, body)
	}
	if locale := s.app.Deps.Settings.GetString(ctx, "site.locale", ""); locale != "fr" {
		t.Fatalf("admin setup overwrote public locale: %s", locale)
	}
	_, dashboard := s.do("GET", "/admin", nil)
	if !strings.Contains(dashboard, `lang="en"`) {
		t.Fatal("admin did not stay English")
	}
}
