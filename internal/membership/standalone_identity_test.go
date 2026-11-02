package membership

import (
	"context"
	"html"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
)

func TestMembershipUsesSavedIdentityWithoutThemeAndKeepsOwnerScope(t *testing.T) {
	s, _, staff, alice, bob, app := setup(t)
	identity := brand.Identity{Accent: "#d7af44", Background: "#142d36", Text: "#f7f5ed", Font: "system", Width: "standard"}
	if err := app.Deps.Settings.Set(staff, "builder.identity", identity); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveGrant(staff, GrantInput{Grant: Grant{OwnerID: actor(alice), PlanID: "paid", Status: "active", PeriodEnd: "2027-01-01T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ctx context.Context
		own bool
	}{{alice, true}, {bob, false}} {
		response := httptest.NewRecorder()
		app.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/membership", nil).WithContext(tc.ctx))
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, "--standalone-accent:#d7af44") || !strings.Contains(body, "--standalone-bg:#142d36") {
			t.Fatalf("identity not rendered: %d", response.Code)
		}
		nonce := regexp.MustCompile(`<style nonce="([^"]+)" data-gorun-standalone-identity>`).FindStringSubmatch(body)
		if len(nonce) != 2 || !strings.Contains(response.Header().Get("Content-Security-Policy"), "'nonce-"+html.UnescapeString(nonce[1])+"'") {
			t.Fatal("standalone style must use the response CSP nonce")
		}
		if strings.Contains(body, "2027-01-01") != tc.own {
			t.Fatal("identity presentation changed membership owner scope")
		}
	}
	if _, err := app.Deps.DB.Exec("DELETE FROM membership_policies"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/membership", nil).WithContext(staff))
	body := response.Body.String()
	if response.Code != 200 || strings.Contains(body, `action="/membership/submissions"`) || !strings.Contains(body, "إرسال المساهمات غير مهيأ بعد") {
		t.Fatal("missing policies must explain setup rather than show an unusable submission form")
	}
}
