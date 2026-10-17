package admin_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

const testPassword = "correct horse battery"

type site struct {
	t   *testing.T
	srv *httptest.Server
	app *kernel.App
	c   *http.Client
}

func boot(t *testing.T, extra ...kernel.Module) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	mods := append([]kernel.Module{settingsmod.New(), auth.New()}, extra...)
	mods = append(mods, admin.New())
	app, err := kernel.Boot(context.Background(), cfg, mods)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &site{t: t, srv: srv, app: app, c: newClient()}
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *site) users() auth.Service {
	svc, ok := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if !ok {
		s.t.Fatal("auth service missing")
	}
	return svc
}

// do sends a request; headers are given as key, value pairs.
func (s *site) do(method, path string, form url.Values, headers ...string) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// post sends form with the session CSRF token.
func (s *site) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	s.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", s.csrf())
	return s.do("POST", path, form, headers...)
}

var (
	csrfFieldRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	csrfMetaRe  = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
)

func (s *site) csrf() string {
	s.t.Helper()
	_, page := s.do("GET", "/admin", nil)
	m := csrfMetaRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no csrf meta in dashboard:\n%s", page)
	}
	return m[1]
}

// createUser creates a user with role and locale ("" keeps the default).
func (s *site) createUser(email, role, locale string) *auth.User {
	s.t.Helper()
	ctx := context.Background()
	u, err := s.users().Create(ctx, email, strings.Split(email, "@")[0], testPassword, role)
	if err != nil {
		s.t.Fatal(err)
	}
	if locale != "" {
		if u, err = s.users().Update(ctx, u.ID, auth.UserPatch{Locale: &locale}); err != nil {
			s.t.Fatal(err)
		}
	}
	return u
}

// login creates the user and signs in with a fresh cookie jar.
func (s *site) login(email, role, locale string) *auth.User {
	s.t.Helper()
	u := s.createUser(email, role, locale)
	s.signIn(email)
	return u
}

func (s *site) signIn(email string) {
	s.t.Helper()
	s.c = newClient()
	_, page := s.do("GET", "/admin/login", nil)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no csrf on login page:\n%s", page)
	}
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {m[1]}, "email": {email}, "password": {testPassword}})
	if resp.StatusCode != http.StatusSeeOther {
		s.t.Fatalf("login %s: %d", email, resp.StatusCode)
	}
}

// auditCount counts audit entries with action (and target id when given).
func (s *site) auditCount(action string, targetID ...string) int {
	s.t.Helper()
	q := `SELECT count(*) FROM audit_log WHERE action = ?`
	args := []any{action}
	if len(targetID) > 0 {
		q += ` AND target_id = ?`
		args = append(args, targetID[0])
	}
	var n int
	if err := s.app.Deps.DB.QueryRow(q, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("body missing %q:\n%s", w, body)
		}
	}
}

func mustNotContain(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(body, w) {
			t.Fatalf("body must not contain %q:\n%s", w, body)
		}
	}
}
