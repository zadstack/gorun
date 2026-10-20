package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

const setupTok = "prod-setup-token-0123456789"

func setupValues(csrf, token string) url.Values {
	v := url.Values{"_csrf": {csrf}, "name": {"Ahmed"}, "email": {"admin@example.com"}, "password": {"correct horse battery"},
		"title": {"Soak Studio"}, "tagline": {"Fresh site"}, "locale": {"en"}}
	if token != "" {
		v.Set("setup_token", token)
	}
	return v
}

func (s *site) userCount() int {
	s.t.Helper()
	var n int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

// I6: in prod without GORUN_SETUP_TOKEN the setup page only explains
// what to do, and a POST creates nothing.
func TestSetupBlockedInProdWithoutToken(t *testing.T) {
	s := bootCfg(t, func(c *config.Config) { c.Env = "prod" }, adminStub{})
	resp, page := s.do("GET", "/admin/setup", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "GORUN_SETUP_TOKEN") || !strings.Contains(page, "gorun user create --admin") {
		t.Fatalf("blocked setup page: %d\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, `name="password"`) {
		t.Fatal("blocked setup must not render the form")
	}
	u, _ := url.Parse(s.srv.URL)
	var csrf string
	for _, c := range s.c.Jar.Cookies(u) {
		if c.Name == "gp_csrf" {
			csrf = c.Value
		}
	}
	resp, body := s.do("POST", "/admin/setup", setupValues(csrf, setupTok))
	if resp.StatusCode != http.StatusForbidden || s.userCount() != 0 || !strings.Contains(body, "gorun user create --admin") {
		t.Fatalf("blocked submit: %d users=%d", resp.StatusCode, s.userCount())
	}
}

// In prod with a token the form asks for it; a wrong or missing token is
// refused (and audited), the right one creates the admin.
func TestSetupRequiresTokenInProd(t *testing.T) {
	s := bootCfg(t, func(c *config.Config) { c.Env = "prod"; c.SetupToken = setupTok }, adminStub{})
	_, page := s.do("GET", "/admin/setup", nil)
	if !strings.Contains(page, `name="setup_token"`) {
		t.Fatalf("setup form must ask for the token:\n%s", page)
	}
	csrf := csrfFrom(t, page)
	for _, bad := range []string{"", "wrong-token-0123456789", setupTok + "x"} {
		resp, body := s.do("POST", "/admin/setup", setupValues(csrf, bad))
		if resp.StatusCode != http.StatusForbidden || s.userCount() != 0 || !strings.Contains(body, `aria-invalid="true"`) {
			t.Fatalf("token %q: %d users=%d", bad, resp.StatusCode, s.userCount())
		}
	}
	var denied int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.setup_denied'`).Scan(&denied)
	if denied != 3 {
		t.Fatalf("setup_denied audit rows = %d", denied)
	}
	resp, _ := s.do("POST", "/admin/setup", setupValues(csrf, setupTok))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/workspace" || s.userCount() != 1 {
		t.Fatalf("right token: %d %q users=%d", resp.StatusCode, resp.Header.Get("Location"), s.userCount())
	}
}

func TestSetupDevHonorsConfiguredToken(t *testing.T) {
	s := bootCfg(t, func(c *config.Config) { c.SetupToken = setupTok }, adminStub{})
	_, page := s.do("GET", "/admin/setup", nil)
	if !strings.Contains(page, `name="setup_token"`) {
		t.Fatal("configured dev token must be requested")
	}
	csrf := csrfFrom(t, page)
	resp, _ := s.do("POST", "/admin/setup", setupValues(csrf, ""))
	if resp.StatusCode != http.StatusForbidden || s.userCount() != 0 {
		t.Fatal("missing token accepted")
	}
	resp, _ = s.do("POST", "/admin/setup", setupValues(csrf, setupTok))
	if resp.StatusCode != http.StatusSeeOther || s.userCount() != 1 {
		t.Fatal("valid dev token rejected")
	}
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "gp_session" {
			return c
		}
	}
	t.Fatalf("no gp_session cookie: %v", resp.Header.Values("Set-Cookie"))
	return nil
}

// Addendum A: an https site.base_url on a plain-http deployment must not
// make the session cookie Secure (the browser would drop it and lock
// everyone out); login keeps working.
func TestSessionCookieIgnoresSiteBaseURLSetting(t *testing.T) {
	s := bootCfg(t, nil, adminStub{})
	if err := s.app.Deps.Settings.Set(context.Background(), "site.base_url", "https://example.org"); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/setup", nil)
	resp, _ := s.do("POST", "/admin/setup", setupValues(csrfFrom(t, page), ""))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup: %d", resp.StatusCode)
	}
	if sessionCookie(t, resp).Secure {
		t.Fatal("gp_session must not be Secure on a plain-http request")
	}
	// The cookie jar sends it back over http: the admin is signed in.
	if resp, _ := s.do("GET", "/admin", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("signed-in admin request: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// Behind a trusted proxy that reports https (the test server's peer is
// loopback), the session cookie is Secure.
func TestSessionCookieSecureForHTTPSRequests(t *testing.T) {
	s := bootCfg(t, func(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1/32", "::1/128"} }, adminStub{})
	_, page := s.do("GET", "/admin/setup", nil)
	req, _ := http.NewRequest("POST", s.srv.URL+"/admin/setup", strings.NewReader(setupValues(csrfFrom(t, page), "").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !sessionCookie(t, resp).Secure {
		t.Fatalf("https request: %d secure=%v", resp.StatusCode, sessionCookie(t, resp).Secure)
	}
}

// After setup, POST /admin/setup redirects to the login page like the GET,
// in prod with or without a token: no token check, no setup_denied rows.
func TestSetupClosedOnceAUserExists(t *testing.T) {
	for name, tok := range map[string]string{"token left configured": setupTok, "no token": ""} {
		s := bootCfg(t, func(c *config.Config) { c.Env = "prod"; c.SetupToken = tok }, adminStub{})
		svc, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
		if _, err := svc.Create(context.Background(), "owner@example.com", "Owner", "correct horse battery", "admin"); err != nil {
			t.Fatal(err)
		}
		_, page := s.do("GET", "/admin/login", nil)
		resp, _ := s.do("POST", "/admin/setup", setupValues(csrfFrom(t, page), "wrong-token-0123456789"))
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login" {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
		var denied int
		s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.setup_denied'`).Scan(&denied)
		if denied != 0 || s.userCount() != 1 {
			t.Fatalf("%s: denied=%d users=%d", name, denied, s.userCount())
		}
	}
}
