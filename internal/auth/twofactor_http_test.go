package auth_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

const tfPassword = "correct horse battery"

var (
	secretRe   = regexp.MustCompile(`data-totp-secret>([A-Z2-7 ]+)<`)
	recoveryRe = regexp.MustCompile(`<code>([0-9a-z]{5}-[0-9a-z]{5})</code>`)
)

// newUser creates an active account through the auth service.
func newUser(t *testing.T, s *site, email, role string) *auth.User {
	t.Helper()
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	u, err := users.Create(context.Background(), email, "Tester", tfPassword, role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// client returns another browser (own cookie jar) on the same server.
func (s *site) client() *site {
	jar, _ := cookiejar.New(nil)
	return &site{t: s.t, srv: s.srv, app: s.app, c: &http.Client{Jar: jar, CheckRedirect: s.c.CheckRedirect}}
}

// login posts the password form and returns the response.
func (s *site) login(email, password string) *http.Response {
	s.t.Helper()
	_, page := s.do("GET", "/admin/login", nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(s.t, page)}, "email": {email}, "password": {password}})
	return resp
}

// enrol turns on two-factor for the signed-in user and returns the setup
// key and recovery codes.
func (s *site) enrol() (string, []string) {
	s.t.Helper()
	_, page := s.do("GET", "/admin/account/two-factor", nil)
	resp, _ := s.do("POST", "/admin/account/two-factor/start", url.Values{"_csrf": {csrfFrom(s.t, page)}})
	if resp.StatusCode != http.StatusSeeOther {
		s.t.Fatalf("start enrolment: %d", resp.StatusCode)
	}
	resp, page = s.do("GET", "/admin/account/two-factor", nil)
	m := secretRe.FindStringSubmatch(page)
	if resp.StatusCode != 200 || m == nil || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(page, "otpauth://totp/") || !strings.Contains(page, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=") || !strings.Contains(page, "data-totp-qr") {
		s.t.Fatalf("enrolment page: %d %q\n%s", resp.StatusCode, resp.Header.Get("Cache-Control"), page)
	}
	key := m[1]
	resp, _ = s.do("POST", "/admin/account/two-factor/confirm", url.Values{"_csrf": {csrfFrom(s.t, page)}, "code": {"000000"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		s.t.Fatalf("wrong confirmation code: %d", resp.StatusCode)
	}
	resp, page = s.do("POST", "/admin/account/two-factor/confirm", url.Values{"_csrf": {csrfFrom(s.t, page)}, "code": {auth.TOTPCodeForTest(key, time.Now())}})
	var codes []string
	for _, m := range recoveryRe.FindAllStringSubmatch(page, -1) {
		codes = append(codes, m[1])
	}
	if resp.StatusCode != 200 || len(codes) != 10 {
		s.t.Fatalf("confirm: %d, %d codes", resp.StatusCode, len(codes))
	}
	return key, codes
}

func (s *site) logout() {
	s.t.Helper()
	_, page := s.do("GET", "/admin/account/two-factor", nil)
	if resp, _ := s.do("POST", "/admin/logout", url.Values{"_csrf": {csrfFrom(s.t, page)}}); resp.StatusCode != http.StatusSeeOther {
		s.t.Fatalf("logout: %d", resp.StatusCode)
	}
}

// secondFactor posts code on the second sign-in step.
func (s *site) secondFactor(code string) (*http.Response, string) {
	s.t.Helper()
	_, page := s.do("GET", auth.SecondFactorPath, nil)
	if !strings.Contains(page, `name="_csrf"`) { // expired step: take the token from another page
		if _, page = s.do("GET", "/admin/login", nil); !strings.Contains(page, `name="_csrf"`) {
			_, page = s.do("GET", "/admin/account/two-factor", nil)
		}
	}
	return s.do("POST", auth.SecondFactorPath, url.Values{"_csrf": {csrfFrom(s.t, page)}, "code": {code}})
}

func auditCount(t *testing.T, s *site, action string) int {
	t.Helper()
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n)
	return n
}

func TestTwoFactorLoginFlow(t *testing.T) {
	s := boot(t)
	newUser(t, s, "tf@example.com", "editor")
	if resp := s.login("tf@example.com", tfPassword); resp.Header.Get("Location") != "/admin" {
		t.Fatalf("plain login: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	key, codes := s.enrol()
	s.logout()

	// The password alone only reaches the second step: no session yet.
	resp := s.login("tf@example.com", tfPassword)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != auth.SecondFactorPath {
		t.Fatalf("login with 2FA: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(r.Header.Get("Location"), "/admin/login") {
		t.Fatalf("pending sign-in reached the admin: %d", r.StatusCode)
	}
	if r, _ := s.secondFactor("123456"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", r.StatusCode)
	}
	// The code used to confirm enrolment is replay-protected; the next
	// step (allowed clock drift) works once.
	next := auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))
	if r, _ := s.secondFactor(next); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin" {
		t.Fatalf("valid code: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("session after second factor: %d", r.StatusCode)
	}
	// The pending sign-in was single use.
	if r, page := s.secondFactor(next); r.StatusCode != http.StatusBadRequest || !strings.Contains(page, `href="/admin/login"`) {
		t.Fatalf("challenge reused: %d", r.StatusCode)
	}
	s.logout()

	// Same code again on a new sign-in: replay refused.
	s.login("tf@example.com", tfPassword)
	if r, _ := s.secondFactor(next); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed code: %d", r.StatusCode)
	}
	// A recovery code works once.
	if r, _ := s.secondFactor(strings.ToUpper(codes[0])); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("recovery code: %d", r.StatusCode)
	}
	s.logout()
	s.login("tf@example.com", tfPassword)
	if r, _ := s.secondFactor(codes[0]); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("recovery code reused: %d", r.StatusCode)
	}
	for _, action := range []string{"user.2fa_enabled", "user.2fa_failed", "user.2fa_recovery_used"} {
		if auditCount(t, s, action) == 0 {
			t.Errorf("no %s audit event", action)
		}
	}
	// No secret or code is ever written to the audit log.
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT group_concat(meta_json) FROM audit_log`).Scan(&meta)
	if strings.Contains(meta, codes[0]) || strings.Contains(meta, next) {
		t.Fatal("a code reached the audit log")
	}
}

func TestTwoFactorChallengeExpiresAndIsRateLimited(t *testing.T) {
	s := boot(t)
	newUser(t, s, "limit@example.com", "editor")
	s.login("limit@example.com", tfPassword)
	key, _ := s.enrol()
	s.logout()

	s.login("limit@example.com", tfPassword)
	// Five minutes later the pending sign-in is gone.
	s.app.Deps.DB.Exec(`UPDATE login_challenges SET expires_at = '2000-01-01T00:00:00Z'`)
	if r, _ := s.secondFactor(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("expired challenge: %d", r.StatusCode)
	}

	s.login("limit@example.com", tfPassword)
	for i := 1; i <= 5; i++ {
		if r, _ := s.secondFactor("000000"); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, r.StatusCode)
		}
	}
	// Five wrong codes end the pending sign-in…
	if r, _ := s.secondFactor("000000"); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("challenge survived five wrong codes: %d", r.StatusCode)
	}
	// …and the per-user limit (shared with password lockout) refuses even a
	// correct code until the window passes.
	s.login("limit@example.com", tfPassword)
	if r, _ := s.secondFactor(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", r.StatusCode)
	}
}

func TestTwoFactorEnforcementByRole(t *testing.T) {
	s := boot(t)
	newUser(t, s, "author@example.com", "author")
	if err := s.app.Deps.Settings.Set(context.Background(), auth.TwoFactorRolesSetting, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	s.login("author@example.com", tfPassword)
	for _, p := range []string{"/admin", "/admin/account", "/admin/navigation"} {
		if r, _ := s.do("GET", p, nil); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin/account/two-factor" {
			t.Fatalf("%s not held for enrolment: %d %s", p, r.StatusCode, r.Header.Get("Location"))
		}
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/admin/navigation", nil)
	req.Header.Set("HX-Request", "true")
	if r, err := s.c.Do(req); err != nil || r.Header.Get("HX-Redirect") != "/admin/account/two-factor" {
		t.Fatal("HTMX request not redirected to enrolment")
	}
	// Polling from the enrolment page itself gets no redirect (no reload loop).
	req.Header.Set("HX-Current-URL", s.srv.URL+"/admin/account/two-factor")
	if r, err := s.c.Do(req); err != nil || r.StatusCode != http.StatusForbidden || r.Header.Get("HX-Redirect") != "" {
		t.Fatal("enrolment page polling redirected")
	}
	if r, _ := s.do("GET", "/api/v1/users", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("session API before enrolment: %d", r.StatusCode)
	}
	r, page := s.do("GET", "/admin/account/two-factor", nil)
	if r.StatusCode != 200 || !strings.Contains(page, "data-two-factor-required") {
		t.Fatalf("enrolment page: %d", r.StatusCode)
	}
	s.enrol()
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("after enrolment: %d", r.StatusCode)
	}
	// Required roles cannot turn it off.
	_, page = s.do("GET", "/admin/account/two-factor", nil)
	if r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {csrfFrom(t, page)}, "password": {tfPassword}, "code": {"000000"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("required factor disabled: %d", r.StatusCode)
	}
	// Other roles are not affected.
	other := s.client()
	newUser(t, s, "editor@example.com", "editor")
	other.login("editor@example.com", tfPassword)
	if r, _ := other.do("GET", "/admin", nil); r.StatusCode != 200 {
		t.Fatalf("editor held: %d", r.StatusCode)
	}
}

func TestTwoFactorDisableNeedsPasswordAndCode(t *testing.T) {
	s := boot(t)
	newUser(t, s, "off@example.com", "editor")
	s.login("off@example.com", tfPassword)
	key, codes := s.enrol()
	_, page := s.do("GET", "/admin/account/two-factor", nil)
	token := csrfFrom(t, page)
	if r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {token}, "password": {"wrong password!"}, "code": {codes[1]}}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {token}, "password": {tfPassword}, "code": {"000000"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("wrong code: %d", r.StatusCode)
	}
	// Regenerating needs a current code and invalidates the old codes.
	r, page := s.do("POST", "/admin/account/two-factor/recovery-codes", url.Values{"_csrf": {token}, "code": {auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))}})
	if r.StatusCode != 200 || len(recoveryRe.FindAllString(page, -1)) != 10 {
		t.Fatalf("regenerate: %d", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {token}, "password": {tfPassword}, "code": {codes[1]}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("old recovery code after regeneration: %d", r.StatusCode)
	}
	fresh := recoveryRe.FindStringSubmatch(page)[1]
	if r, _ := s.do("POST", "/admin/account/two-factor/disable", url.Values{"_csrf": {token}, "password": {tfPassword}, "code": {fresh}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable: %d", r.StatusCode)
	}
	if auditCount(t, s, "user.2fa_disabled") != 1 {
		t.Fatal("disable not audited")
	}
	s.logout()
	if r := s.login("off@example.com", tfPassword); r.Header.Get("Location") != "/admin" {
		t.Fatalf("login after disable: %s", r.Header.Get("Location"))
	}
}

func TestMagicLinkAndStartUserSessionRequireSecondFactor(t *testing.T) {
	s := boot(t)
	u := newUser(t, s, "magic@example.com", "editor")
	s.login("magic@example.com", tfPassword)
	key, _ := s.enrol()
	s.logout()

	token, err := auth.NewMagicLink(context.Background(), s.app.Deps, u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.do("POST", "/auth/magic/"+token, url.Values{})
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != auth.SecondFactorPath {
		t.Fatalf("magic link skipped the second factor: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := s.do("GET", "/admin", nil); r.StatusCode != http.StatusSeeOther {
		t.Fatal("magic link alone created a session")
	}
	if r, _ := s.secondFactor(auth.TOTPCodeForTest(key, time.Now().Add(30*time.Second))); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/account" {
		t.Fatalf("magic link second step: %d %s", r.StatusCode, r.Header.Get("Location"))
	}

	// Other modules' password forms (StartUserSession) get the same step.
	rec := &cookieRecorder{header: http.Header{}}
	req, _ := http.NewRequest("POST", "/account/login", nil)
	if err := auth.StartUserSession(s.app.Deps, rec, req, u); err != auth.ErrSecondFactorRequired {
		t.Fatalf("StartUserSession: %v", err)
	}
	if strings.Contains(strings.Join(rec.header.Values("Set-Cookie"), ";"), "gp_session=") {
		t.Fatal("StartUserSession set a session cookie for a 2FA user")
	}
}

type cookieRecorder struct {
	header http.Header
	status int
}

func (c *cookieRecorder) Header() http.Header         { return c.header }
func (c *cookieRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c *cookieRecorder) WriteHeader(s int)           { c.status = s }
