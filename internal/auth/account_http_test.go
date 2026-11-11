package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

var revokeRe = regexp.MustCompile(`action="/admin/account/sessions/([0-9a-f]{64})/revoke"`)

func TestAccountProfileEmailAndPassword(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "me@example.com", "editor")
	newUser(t, s, "taken@example.com", "editor")
	s.login("me@example.com", tfPassword)
	r, page := s.do("GET", "/admin/account", nil)
	if r.StatusCode != 200 || !strings.Contains(page, `action="/admin/account/profile"`) || !strings.Contains(page, `data-account-link`) {
		t.Fatalf("account page: %d", r.StatusCode)
	}
	token := csrfFrom(t, page)

	if r, _ := s.do("POST", "/admin/account/profile", url.Values{"name": {"No CSRF"}, "locale": {"en"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("profile without CSRF: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/profile", url.Values{"_csrf": {token}, "name": {"Mona"}, "locale": {"xx"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad locale: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/profile", url.Values{"_csrf": {token}, "name": {"Mona"}, "locale": {"en"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("profile: %d", r.StatusCode)
	}
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if got, _ := users.Get(context.Background(), u.ID); got.Name != "Mona" || got.Locale != "en" {
		t.Fatalf("profile not saved: %+v", got)
	}

	// Email: needs the current password and keeps uniqueness.
	if r, _ := s.do("POST", "/admin/account/email", url.Values{"_csrf": {token}, "email": {"new@example.com"}, "current_password": {"wrong password!"}}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("email with wrong password: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/email", url.Values{"_csrf": {token}, "email": {"Taken@example.com"}, "current_password": {tfPassword}}); r.StatusCode != http.StatusConflict {
		t.Fatalf("taken email: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/email", url.Values{"_csrf": {token}, "email": {"new@example.com"}, "current_password": {tfPassword}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("email: %d", r.StatusCode)
	}
	if got, _ := users.Get(context.Background(), u.ID); got.Email != "new@example.com" {
		t.Fatalf("email not changed: %s", got.Email)
	}

	// Password: a second device is signed out, this one stays.
	other := s.client()
	other.login("new@example.com", tfPassword)
	if r, _ := other.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatal("second device not signed in")
	}
	if r, _ := s.do("POST", "/admin/account/password", url.Values{"_csrf": {token}, "current_password": {tfPassword}, "new_password": {"short"}, "confirm_password": {"short"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("short password: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/password", url.Values{"_csrf": {token}, "current_password": {"wrong password!"}, "new_password": {"another long password"}, "confirm_password": {"another long password"}}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong current password: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/password", url.Values{"_csrf": {token}, "current_password": {tfPassword}, "new_password": {"another long password"}, "confirm_password": {"another long password"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("password: %d", r.StatusCode)
	}
	if r, _ := s.do("GET", "/admin/account", nil); r.StatusCode != 200 {
		t.Fatal("current session lost after password change")
	}
	if r, _ := other.do("GET", "/admin", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatal("other session survived the password change")
	}
	if _, err := users.CheckPassword(context.Background(), "new@example.com", "another long password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	for _, action := range []string{"user.update", "user.email_changed", "user.password_changed"} {
		if auditCount(t, s, action) == 0 {
			t.Errorf("no %s audit", action)
		}
	}
}

func TestAccountSessionsRevoke(t *testing.T) {
	s := boot(t)
	newUser(t, s, "devices@example.com", "editor")
	s.login("devices@example.com", tfPassword)
	phone, laptop := s.client(), s.client()
	phone.login("devices@example.com", tfPassword)
	laptop.login("devices@example.com", tfPassword)

	_, page := s.do("GET", "/admin/account", nil)
	if n := strings.Count(page, "data-session"); n < 3 || strings.Count(page, "data-session-current") != 1 {
		t.Fatalf("session rows: %d", n)
	}
	ids := revokeRe.FindAllStringSubmatch(page, -1)
	if len(ids) != 2 {
		t.Fatalf("revoke buttons: %d (the current session must not have one)", len(ids))
	}
	token := csrfFrom(t, page)
	// Another user's session id cannot be revoked from this account.
	newUser(t, s, "stranger@example.com", "editor")
	stranger := s.client()
	stranger.login("stranger@example.com", tfPassword)
	_, spage := stranger.do("GET", "/admin/account", nil)
	ownerSession := revokeRe.FindStringSubmatch(page)[1]
	if r, _ := stranger.do("POST", "/admin/account/sessions/"+ownerSession+"/revoke", url.Values{"_csrf": {csrfFrom(t, spage)}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("foreign revoke: %d", r.StatusCode)
	}
	alive := 0
	for _, c := range []*site{phone, laptop} {
		if r, _ := c.do("GET", "/admin", nil); r.StatusCode == 200 {
			alive++
		}
	}
	if alive != 2 {
		t.Fatalf("another account revoked a session: %d alive", alive)
	}

	if r, _ := s.do("POST", "/admin/account/sessions/"+ids[0][1]+"/revoke", url.Values{"_csrf": {token}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	alive = 0
	for _, c := range []*site{phone, laptop} {
		if r, _ := c.do("GET", "/admin", nil); r.StatusCode == 200 {
			alive++
		}
	}
	if alive != 1 {
		t.Fatalf("after one revoke %d devices alive", alive)
	}
	if r, _ := s.do("POST", "/admin/account/sessions/revoke-others", url.Values{"_csrf": {token}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke others: %d", r.StatusCode)
	}
	for _, c := range []*site{phone, laptop} {
		if r, _ := c.do("GET", "/admin", nil); r.StatusCode == 200 {
			t.Fatal("a device survived sign out other devices")
		}
	}
	if r, _ := s.do("GET", "/admin/account", nil); r.StatusCode != 200 {
		t.Fatal("current device signed out")
	}
	if auditCount(t, s, "user.session_revoked") != 1 || auditCount(t, s, "user.sessions_revoked") != 1 {
		t.Fatal("session revocations not audited")
	}
}

func TestAccountRefusesAPIKeys(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "keyowner@example.com", "admin")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	plain, _, err := keys.Create(auth.WithUser(context.Background(), u), "k", u.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/admin/account", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	r, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode == 200 {
		t.Fatal("API key opened the account page")
	}
}
