package auth_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// startElsewhere signs u in through another module's password form
// (StartUserSessionNext) with next as the requested return path, and moves
// the pending sign-in cookie into the browser s.
func (s *site) startElsewhere(u *auth.User, next string) {
	s.t.Helper()
	rec := &cookieRecorder{header: http.Header{}}
	req, _ := http.NewRequest("POST", s.srv.URL+"/account/password-login", nil)
	if err := auth.StartUserSessionNext(s.app.Deps, rec, req, u, next); err != auth.ErrSecondFactorRequired {
		s.t.Fatalf("StartUserSessionNext: %v", err)
	}
	cookies := (&http.Response{Header: rec.header}).Cookies()
	target, _ := url.Parse(s.srv.URL + auth.SecondFactorPath)
	s.c.Jar.SetCookies(target, cookies)
}

// A shop or launch sign-in that needs the second step comes back to where
// it started, never to another site.
func TestSecondFactorReturnsToTheStartingForm(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "shop-staff@example.com", "editor")
	s.login("shop-staff@example.com", tfPassword)
	key, codes := s.enrol()
	s.logout()

	s.startElsewhere(u, "/agents?tab=runs")
	if r, _ := s.secondFactor(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/agents?tab=runs" {
		t.Fatalf("return path lost: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	s.logout()

	attacks := []string{"https://evil.example/", "//evil.example", "/\\evil.example", "/%0d%0aSet-Cookie:x", "/\tevil", "javascript:alert(1)", "evil.example", ""}
	for i, next := range attacks {
		s.startElsewhere(u, next)
		r, _ := s.secondFactor(codes[i])
		if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin" {
			t.Fatalf("next %q: %d %s, want /admin", next, r.StatusCode, r.Header.Get("Location"))
		}
		s.logout()
	}
}

// After a GORUN_SECRET_KEY change an app code is refused like a wrong one
// (401, not 500) and a recovery code still signs in.
func TestSecondFactorWithRotatedSecretKey(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "rotated@example.com", "editor")
	s.login("rotated@example.com", tfPassword)
	key, codes := s.enrol()
	s.logout()

	// Re-seal the secret under another key: what the server sees after
	// the operator replaced GORUN_SECRET_KEY.
	enc, err := db.Encrypt(bytes.Repeat([]byte{9}, 32), []byte("12345678901234567890"), "auth.totp:"+u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.app.Deps.DB.Exec(`UPDATE user_totp SET secret_enc = ? WHERE user_id = ?`, enc, u.ID); err != nil {
		t.Fatal(err)
	}
	s.login("rotated@example.com", tfPassword)
	if r, _ := s.secondFactor(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("app code with unreadable secret: %d, want 401", r.StatusCode)
	}
	if r, _ := s.secondFactor(codes[0]); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin" {
		t.Fatalf("recovery code after key rotation: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
}
