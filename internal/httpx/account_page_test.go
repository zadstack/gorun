package httpx

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestAccountSignInUnavailableFollowsRequestLocaleAndKeepsRecoveryActions(t *testing.T) {
	for _, scenario := range []struct{ locale, direction, title string }{{"ar", "rtl", "الدخول غير متاح"}, {"en", "ltr", "Sign-in unavailable"}} {
		t.Run(scenario.locale, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/membership", nil).WithContext(i18n.WithLocale(context.Background(), scenario.locale))
			opposite := "en"
			if scenario.locale == "en" {
				opposite = "ar"
			}
			request.Header.Set("Accept-Language", opposite)
			response := httptest.NewRecorder()
			AccountSignInUnavailable(response, request)
			if response.Code != 503 || response.Header().Get("Location") != "" {
				t.Fatalf("unavailable response: %d %v", response.Code, response.Header())
			}
			for _, expected := range []string{`<html lang="` + scenario.locale + `" dir="` + scenario.direction + `">`, `<title>` + scenario.title + `</title>`, `href="/"`, `href="/admin/login"`, `account_sign_in_unavailable`, "Customer sign-in is unavailable", "بيانات العضويات والاشتراكات محفوظة"} {
				if !strings.Contains(response.Body.String(), expected) {
					t.Errorf("missing %q", expected)
				}
			}
		})
	}
}
