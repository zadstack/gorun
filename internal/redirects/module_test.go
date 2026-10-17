package redirects_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

type site struct {
	t    *testing.T
	srv  *httptest.Server
	app  *kernel.App
	c    *http.Client
	csrf string
}

func boot(t *testing.T, role string) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), redirects.New()})
	if err != nil {
		t.Fatal(err)
	}
	api := chi.NewRouter()
	api.Use(auth.APIKeyMiddleware(app.Deps))
	for _, op := range app.Deps.API.Operations() {
		api.Method(op.Method, op.Path, op.Handler)
	}
	root := chi.NewRouter()
	root.Mount("/api", api)
	root.Mount("/", app.Handler)
	srv := httptest.NewServer(root)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, srv: srv, app: app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "u@example.com", "U", "correct horse battery", role); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil, "")
	s.csrf = csrfFrom(t, page)
	if resp, _ := s.form("/admin/login", url.Values{"email": {"u@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil, "")
	s.csrf = csrfFrom(t, page)
	return s
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no csrf meta in:\n%s", html)
	}
	return m[1]
}

func (s *site) do(method, path string, body io.Reader, ctype string, hdr ...string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func (s *site) form(path string, v url.Values) (*http.Response, string) {
	v.Set("_csrf", s.csrf)
	return s.do("POST", path, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}

func TestAdminRedirectsFlowAndPublicMiddleware(t *testing.T) {
	s := boot(t, "admin")
	resp, page := s.do("GET", "/admin/redirects", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(page, `name="from_path"`) || !strings.Contains(page, `hx-encoding="multipart/form-data"`) {
		t.Fatalf("list: %d\n%s", resp.StatusCode, page)
	}
	if resp, page := s.form("/admin/redirects", url.Values{"from_path": {"/x"}, "to_url": {"javascript:1"}, "status": {"301"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("invalid add: %d", resp.StatusCode)
	}
	if resp, _ := s.form("/admin/redirects", url.Values{"from_path": {"/old-blog/"}, "to_url": {"/blog"}, "status": {"308"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add: %d", resp.StatusCode)
	}
	// The middleware wraps the public router (served by the booted app).
	resp, _ = s.do("GET", "/old-blog?p=2", nil, "")
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "/blog?p=2" {
		t.Fatalf("public redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// CSV import through HTMX multipart.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "r.csv")
	fw.Write([]byte("from,to,status\n/a,/b\n/bad,ftp://x\n"))
	mw.Close()
	resp, page = s.do("POST", "/admin/redirects/import", &buf, mw.FormDataContentType(), "HX-Request", "true")
	if resp.StatusCode != 200 || !strings.Contains(page, `role="status"`) || strings.Contains(page, "<html") {
		t.Fatalf("import: %d\n%s", resp.StatusCode, page)
	}
	svc, _ := kernel.Get[redirects.Service](s.app.Deps, kernel.ServiceRedirects)
	items, total, _ := svc.List(context.Background(), redirects.ListQuery{Sort: "from_path", Order: "asc"})
	if total != 2 || items[0].FromPath != "/a" {
		t.Fatalf("after import: %+v", items)
	}
	if resp, _ := s.form("/admin/redirects/"+items[0].ID+"/toggle", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("toggle: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/a", nil, ""); resp.StatusCode == http.StatusMovedPermanently {
		t.Fatal("disabled redirect still served")
	}
	if resp, _ := s.form("/admin/redirects/"+items[0].ID+"/delete", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	// API.
	resp, out := s.do("POST", "/api/v1/redirects", strings.NewReader(`{"from_path":"/api-old","to_url":"https://example.com/"}`), "application/json")
	if resp.StatusCode != http.StatusCreated || !strings.Contains(out, `"enabled":true`) || !strings.Contains(out, `"status":301`) {
		t.Fatalf("api create: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("GET", "/api/v1/redirects?search=api-old", nil, ""); resp.StatusCode != 200 || !strings.Contains(out, `"total":1`) {
		t.Fatalf("api list: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("POST", "/api/v1/redirects", strings.NewReader(`{"from_path":"/api-old","to_url":"/x"}`), "application/json"); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(out, "from_taken") {
		t.Fatalf("api dup: %d %s", resp.StatusCode, out)
	}
}

func TestAdminRedirectsRequirePermission(t *testing.T) {
	// author lacks redirects.manage in the default role seed
	s := boot(t, "author")
	if resp, _ := s.do("GET", "/admin/redirects", nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/api/v1/redirects", nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author api: %d", resp.StatusCode)
	}
	// editor is granted redirects.manage by default (auth migration 0002)
	ed := boot(t, "editor")
	if resp, _ := ed.do("GET", "/admin/redirects", nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
	if resp, _ := ed.do("GET", "/api/v1/redirects", nil, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("editor api: %d", resp.StatusCode)
	}
}
