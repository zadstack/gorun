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

func TestAccountSignInUnavailableNormalizesLocaleAndVariesByLanguage(t *testing.T) {
	for _, tc := range []struct{ name, contextLocale, header, want string }{
		{"regional context", "en-US", "ar", "en"},
		{"uppercase context", "EN", "ar", "en"},
		{"unknown context header", "fr", "en-US", "en"},
		{"unknown context default", "fr", "de", "ar"},
		{"weighted header", "", "ar;q=0.2,en-US;q=0.9", "en"},
		{"weighted Arabic", "unknown", "en;q=0.1,ar;q=0.8", "ar"},
		{"explicit context wins", "ar", "en;q=1", "ar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/membership", nil)
			if tc.contextLocale != "" {
				r = r.WithContext(i18n.WithLocale(r.Context(), tc.contextLocale))
			}
			r.Header.Set("Accept-Language", tc.header)
			w := httptest.NewRecorder()
			w.Header().Set("Vary", "Origin")
			AccountSignInUnavailable(w, r)
			direction, title := "rtl", "الدخول غير متاح"
			if tc.want == "en" {
				direction, title = "ltr", "Sign-in unavailable"
			}
			if w.Code != 503 || !strings.Contains(w.Body.String(), `<html lang="`+tc.want+`" dir="`+direction+`">`) || !strings.Contains(w.Body.String(), `<title>`+title+`</title>`) {
				t.Fatalf("locale response: %d %s", w.Code, w.Body.String())
			}
			vary := strings.Join(w.Header().Values("Vary"), ",")
			if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Language") {
				t.Fatalf("Vary=%q", vary)
			}
			for _, action := range []string{`href="/"`, `href="/admin/login"`, "account_sign_in_unavailable"} {
				if !strings.Contains(w.Body.String(), action) {
					t.Errorf("missing %s", action)
				}
			}
		})
	}
}
