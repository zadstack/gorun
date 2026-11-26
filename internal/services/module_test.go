package services_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/services"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
	"github.com/zadstack/gorun/web"
)

type pageEnv struct {
	t   *testing.T
	app *kernel.App
	srv *httptest.Server
	c   *http.Client
}

func bootPage(t *testing.T) *pageEnv {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://site.test", Theme: "default"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir("../../themes")), site.New(), api.New(), admin.New(), services.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &pageEnv{t: t, app: app, srv: srv}
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// as is a client signed in as a new user with role.
func (e *pageEnv) as(email, role string) *pageEnv {
	e.t.Helper()
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), email, "User", "correct horse battery", role); err != nil {
		e.t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	other := *e
	other.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := url.Values{"_csrf": {other.csrf("/admin/login")}, "email": {email}, "password": {"correct horse battery"}}
	if resp, _ := other.post("/admin/login", form); resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login %s: %d", email, resp.StatusCode)
	}
	return &other
}

func (e *pageEnv) do(method, path string, body io.Reader) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	req.Header.Set("Accept-Language", "en")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (e *pageEnv) get(path string) (*http.Response, string) { return e.do("GET", path, nil) }
func (e *pageEnv) post(path string, f url.Values) (*http.Response, string) {
	return e.do("POST", path, strings.NewReader(f.Encode()))
}

func (e *pageEnv) csrf(path string) string {
	e.t.Helper()
	_, page := e.get(path)
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no _csrf on %s:\n%s", path, page)
	}
	return m[1]
}

func TestSecretPageNeedsSettingsManage(t *testing.T) {
	e := bootPage(t)
	ctx := context.Background()
	d := e.app.Deps
	reg := services.NewRegistry(d, services.Mail(d))
	smtp := map[string]string{"provider": "smtp", "host": "smtp.example.com", "port": "2525", "tls": "tls", "username": "mailer", "from": "site@example.com"}
	if _, fields, err := reg.Set(ctx, "mail", smtp, nil, "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("set mail: %v %v", fields, err)
	}
	link, _, err := reg.SecretLink(ctx, "mail", "agent")
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(link, "/admin/services/mail/secret?t=")

	// Signed out: sent to the login page.
	jar, _ := cookiejar.New(nil)
	anon := *e
	anon.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, _ := anon.get(link); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
		t.Fatalf("signed out: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// An editor has no settings.manage: 403 on both methods, nothing saved.
	ed := e.as("ed@example.com", "editor")
	if resp, _ := ed.get(link); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor GET: %d, want 403", resp.StatusCode)
	}
	edCSRF := ed.csrf("/admin/account")
	if resp, _ := ed.post("/admin/services/mail/secret", url.Values{"_csrf": {edCSRF}, "t": {tok}, "secret": {"ed-pass"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor POST: %d, want 403", resp.StatusCode)
	}

	// An admin sees one password field, then saves once.
	ad := e.as("admin@example.com", "admin")
	resp, page := ad.get(link)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin GET: %d\n%s", resp.StatusCode, page)
	}
	if n := strings.Count(page, `type="password"`); n != 1 {
		t.Fatalf("%d password fields, want 1", n)
	}
	if !strings.Contains(page, `autocomplete="off"`) || !strings.Contains(page, "password") {
		t.Fatalf("page has no autocomplete=off password field:\n%s", page)
	}
	if !strings.Contains(page, "Type the SMTP password for Mail.") {
		t.Errorf("page does not say what the secret is for:\n%s", page)
	}
	if rp := resp.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy %q, want no-referrer", rp)
	}
	csrf := csrfRe.FindStringSubmatch(page)[1]

	// Without CSRF: refused.
	if resp, _ := ad.post("/admin/services/mail/secret", url.Values{"t": {tok}, "secret": {"no-csrf"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF: %d, want 403", resp.StatusCode)
	}
	resp, page = ad.post("/admin/services/mail/secret", url.Values{"_csrf": {csrf}, "t": {tok}, "secret": {"top-s3cret-pass"}})
	if resp.StatusCode != http.StatusOK || strings.Contains(page, "top-s3cret-pass") || strings.Contains(page, `type="password"`) {
		t.Fatalf("admin POST: %d\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(page, "The secret is saved") || strings.Contains(page, tok) {
		t.Fatalf("success page:\n%s", page)
	}
	mail, _ := reg.Get("mail")
	if s, _ := mail.Status(ctx); !s.SecretSet || s.State != services.StateConfigured {
		t.Fatalf("mail after the page: %+v", s)
	}
	resp, page = ad.post("/admin/services/mail/secret", url.Values{"_csrf": {csrf}, "t": {tok}, "secret": {"again"}})
	if resp.StatusCode != http.StatusGone || strings.Contains(page, `type="password"`) {
		t.Fatalf("second POST: %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := ad.get(link); resp.StatusCode != http.StatusGone {
		t.Fatalf("used link GET: %d, want 410", resp.StatusCode)
	}
}

// TestServicesCatalogCoversSecretPage checks ar/en parity and that every
// services.* key of the page, each service name and each secret name exists.
func TestServicesCatalogCoversSecretPage(t *testing.T) {
	load := func(locale string) map[string]string {
		b, err := os.ReadFile(filepath.Join("catalog", "services."+locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	en, ar := load("en"), load("ar")
	for k := range en {
		if _, ok := ar[k]; !ok {
			t.Errorf("ar missing %s", k)
		}
	}
	for k := range ar {
		if _, ok := en[k]; !ok {
			t.Errorf("en missing %s", k)
		}
	}
	page, err := fs.ReadFile(web.FS, "admin/templates/pages/services/secret.html")
	if err != nil {
		t.Fatal(err)
	}
	used := []string{}
	for _, m := range regexp.MustCompile(`\bt "services\.([a-z_.]+)"`).FindAllStringSubmatch(string(page), -1) {
		used = append(used, m[1])
	}
	for _, n := range []string{"mail", "storage", "chat", "analytics", "backups"} {
		used = append(used, "name."+n)
	}
	for _, s := range []string{"password", "api_key", "keys"} {
		used = append(used, "secret_name."+s)
	}
	for _, k := range used {
		if _, ok := en[k]; !ok {
			t.Errorf("catalog missing services.%s", k)
		}
	}
}
