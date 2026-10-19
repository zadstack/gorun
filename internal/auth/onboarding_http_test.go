package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestBootstrapFirstLoginMustFinishOnboarding(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	original := "initial strong random password"
	u, err := auth.BootstrapAdmin(ctx, s.app.Deps.DB, s.app.Deps.Audit, original)
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"admin"}, "password": {original}, "next": {"/admin/settings"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin/onboarding" {
		t.Fatalf("first login escaped onboarding: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, target := range []string{"/admin", "/admin/settings", "/admin/navigation"} {
		resp, _ = s.do("GET", target, nil)
		if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin/onboarding" {
			t.Fatalf("gate %s: %d", target, resp.StatusCode)
		}
	}
	resp, _ = s.do("GET", "/api/v1/users", nil)
	if resp.StatusCode != 403 {
		t.Fatalf("unfinished session API: %d", resp.StatusCode)
	}
	resp, page = s.do("GET", "/admin/onboarding", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `dir="rtl"`) {
		t.Fatal("onboarding page unavailable or not RTL")
	}
	// Locale selection is available before setup; labels and layout follow it.
	_, _ = s.do("POST", "/admin/locale", url.Values{"_csrf": {csrfFrom(t, page)}, "locale": {"en"}})
	resp, page = s.do("GET", "/admin/onboarding", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `dir="ltr"`) || !strings.Contains(page, "Site name") {
		t.Fatal("English onboarding unavailable")
	}
	token := csrfFrom(t, page)
	values := url.Values{"_csrf": {token}, "name": {"Owner"}, "email": {"owner@example.com"}, "title": {"My site"}, "locale": {"en"}, "password": {"replacement secure password"}, "confirm_password": {"replacement secure password"}}
	resp, _ = s.do("POST", "/admin/onboarding", url.Values{"email": {"owner@example.com"}})
	if resp.StatusCode != 403 {
		t.Fatalf("missing CSRF: %d", resp.StatusCode)
	}
	values.Set("confirm_password", "mismatch")
	resp, page = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 422 || strings.Contains(page, "replacement secure password") {
		t.Fatal("invalid confirmation accepted or password reflected")
	}
	values.Set("password", original)
	values.Set("confirm_password", original)
	resp, _ = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 422 {
		t.Fatal("temporary password was kept")
	}
	values.Set("password", "replacement secure password")
	values.Set("confirm_password", "replacement secure password")
	parsed, _ := url.Parse(s.srv.URL)
	oldCookies := s.c.Jar.Cookies(parsed)
	resp, _ = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("complete: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page = s.do("GET", "/admin", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `lang="en"`) {
		t.Fatalf("dashboard/locale: %d", resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.title", ""); got != "My site" {
		t.Fatalf("title=%s", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.locale", ""); got != "en" {
		t.Fatalf("locale=%s", got)
	}
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	for _, identity := range []string{"admin", "owner@example.com"} {
		got, err := users.CheckPassword(ctx, identity, "replacement secure password")
		if err != nil || got.ID != u.ID {
			t.Fatalf("new identity rejected: %v", err)
		}
	}
	if _, err := users.CheckPassword(ctx, "admin", original); err == nil {
		t.Fatal("temporary password still accepted")
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/admin", nil)
	for _, c := range oldCookies {
		req.AddCookie(c)
	}
	oldClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	oldResp, err := oldClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	oldResp.Body.Close()
	if oldResp.StatusCode != 303 || !strings.HasPrefix(oldResp.Header.Get("Location"), "/admin/login") {
		t.Fatal("old session survived password replacement")
	}
	resp, _ = s.do("GET", "/admin/onboarding", nil)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" {
		t.Fatal("completed onboarding offered again")
	}
}

func TestExistingAccountSkipsOnboarding(t *testing.T) {
	s := boot(t)
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(context.Background(), "owner@example.com", "Owner", "existing secure password", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"owner@example.com"}, "password": {"existing secure password"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" {
		t.Fatal("existing user forced through onboarding")
	}
}

func TestOnboardingRollsBackAndRejectsReplays(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	u, err := auth.BootstrapAdmin(ctx, s.app.Deps.DB, s.app.Deps.Audit, "temporary secure password")
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"admin"}, "password": {"temporary secure password"}})
	_, page = s.do("GET", "/admin/onboarding", nil)
	values := url.Values{"_csrf": {csrfFrom(t, page)}, "name": {"Owner"}, "email": {"owner@example.com"}, "title": {"Saved title"}, "locale": {"ar"}, "password": {"new secure password"}, "confirm_password": {"new secure password"}}
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(ctx, "taken@example.com", "Other", "other secure password", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	values.Set("email", "taken@example.com")
	resp, _ := s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 422 {
		t.Fatalf("duplicate email: %d", resp.StatusCode)
	}
	values.Set("email", "owner@example.com")
	if _, err := s.app.Deps.DB.Exec(`CREATE TRIGGER fail_onboard_settings BEFORE INSERT ON settings WHEN NEW.key = 'site.title' BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	resp, _ = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 500 {
		t.Fatalf("expected failed transaction: %d", resp.StatusCode)
	}
	original, err := users.Get(ctx, u.ID)
	if err != nil || original.Email != "admin@localhost.invalid" {
		t.Fatal("failed transaction changed email")
	}
	if _, err := users.CheckPassword(ctx, "admin", "temporary secure password"); err != nil {
		t.Fatal("failed transaction changed password")
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.locale", ""); got != "" {
		t.Fatal("failed transaction changed locale")
	}
	if _, err := s.app.Deps.DB.Exec(`DROP TRIGGER fail_onboard_settings`); err != nil {
		t.Fatal(err)
	}
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	key, _, err := keys.Create(ctx, "Before onboarding", u.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("API key bypassed onboarding")
	}
	resp, _ = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 303 {
		t.Fatalf("valid retry: %d", resp.StatusCode)
	}
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("old API key was not revoked")
	}
	values.Set("title", "Replay title")
	resp, _ = s.do("POST", "/admin/onboarding", values)
	if resp.StatusCode != 403 && resp.StatusCode != 303 {
		t.Fatalf("replay status: %d", resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.title", ""); got != "Saved title" {
		t.Fatal("replay rewrote initial settings")
	}
	var events int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.onboarding_complete'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("completion audit count=%d err=%v", events, err)
	}
}

func TestOnboardingRequiresRecentAuthentication(t *testing.T) {
	s := boot(t)
	if _, err := auth.BootstrapAdmin(context.Background(), s.app.Deps.DB, s.app.Deps.Audit, "temporary secure password"); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil)
	s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"admin"}, "password": {"temporary secure password"}})
	_, page = s.do("GET", "/admin/onboarding", nil)
	if _, err := s.app.Deps.DB.Exec(`UPDATE sessions SET reauth_at = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.do("POST", "/admin/onboarding", url.Values{"_csrf": {csrfFrom(t, page)}})
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/reauth") {
		t.Fatal("old session could complete first-login setup")
	}
	resp, _ = s.do("GET", "/admin/reauth", nil)
	if resp.StatusCode != 200 {
		t.Fatal("onboarding gate blocked reauthentication")
	}
}
