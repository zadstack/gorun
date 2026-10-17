package menus_test

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/zadstack/gorun/internal/menus"
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
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), menus.New()})
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
	_, page := s.do("GET", "/admin/login", "", "")
	s.csrf = csrfFrom(t, page)
	if resp, _ := s.form("/admin/login", url.Values{"email": {"u@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin", "", "")
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

func (s *site) do(method, path, body, ctype string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
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
	return s.do("POST", path, v.Encode(), "application/x-www-form-urlencoded")
}

func TestAdminMenuEditorFlow(t *testing.T) {
	s := boot(t, "editor")
	resp, page := s.do("GET", "/admin/menus", "", "")
	if resp.StatusCode != 200 || !strings.Contains(page, `action="/admin/menus"`) || !strings.Contains(page, `value="primary"`) {
		t.Fatalf("list: %d\n%s", resp.StatusCode, page)
	}
	if resp, page := s.form("/admin/menus", url.Values{"name": {""}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("invalid create: %d", resp.StatusCode)
	}
	resp, _ = s.form("/admin/menus", url.Values{"name": {"Main"}, "location": {"primary"}})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/menus/") {
		t.Fatalf("create: %d %q", resp.StatusCode, loc)
	}
	resp, page = s.do("GET", loc, "", "")
	if resp.StatusCode != 200 || !strings.Contains(page, "/static/admin/menu-editor.js") || !strings.Contains(page, `data-items="[]"`) {
		t.Fatalf("editor: %d\n%s", resp.StatusCode, page)
	}
	tree := `[{"label":"Home","kind":"custom","url":"/","children":[{"label":"Sub","url":"/sub","children":[]}]}]`
	if resp, _ := s.form(loc+"/items", url.Values{"items": {tree}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save items: %d", resp.StatusCode)
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	m, err := svc.ByLocation(context.Background(), "primary")
	if err != nil || len(m.Items) != 1 || len(m.Items[0].Children) != 1 || m.Items[0].Children[0].URL != "/sub" {
		t.Fatalf("stored: %+v %v", m, err)
	}
	// The editor gets the flattened tree with depths.
	_, page = s.do("GET", loc, "", "")
	if !strings.Contains(page, `&#34;depth&#34;:1`) {
		t.Fatalf("editor items not flattened:\n%s", page)
	}
	if resp, page := s.form(loc+"/items", url.Values{"items": {`[{"label":"x","url":"javascript:alert(1)"}]`}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, `role="alert"`) {
		t.Fatalf("invalid items: %d", resp.StatusCode)
	}
	if _, page := s.do("GET", loc, "", ""); !strings.Contains(page, `name="locale"`) || !strings.Contains(page, `<option value="en"`) {
		t.Fatalf("no locale select:\n%s", page)
	}
	if resp, _ := s.form(loc, url.Values{"name": {"Main nav"}, "slug": {"main-nav"}, "location": {"footer"}, "locale": {"en"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	if m, _ := svc.Get(context.Background(), "main-nav"); m == nil || m.Location != "footer" || m.Locale != "en" || len(m.Items) != 1 {
		t.Fatalf("settings update lost items: %+v", m)
	}
	if resp, page := s.do("GET", "/admin/x/menus/search?kind=content&q=a", "", ""); resp.StatusCode != 200 || !strings.Contains(page, "غير متاح") {
		t.Fatalf("search without content service: %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := s.form(loc+"/delete", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", loc, "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted editor: %d", resp.StatusCode)
	}
}

func TestAdminMenusRequirePermission(t *testing.T) {
	s := boot(t, "author")
	if resp, _ := s.do("GET", "/admin/menus", "", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author: %d", resp.StatusCode)
	}
}

func TestMenusAPI(t *testing.T) {
	s := boot(t, "admin")
	resp, out := s.do("POST", "/api/v1/menus", `{"name":"Footer","location":"footer","items":[{"label":"Contact","url":"mailto:a@b.co"}]}`, "application/json")
	var env struct {
		Data menus.Menu `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || resp.StatusCode != http.StatusCreated || env.Data.Slug != "footer" || len(env.Data.Items) != 1 {
		t.Fatalf("create: %d %s", resp.StatusCode, out)
	}
	id := env.Data.ID
	if resp, out := s.do("PUT", "/api/v1/menus/"+id+"/items", `{"items":[{"label":"A","url":"/a"},{"label":"B","url":"/b"}]}`, "application/json"); resp.StatusCode != 200 || !strings.Contains(out, `"label":"B"`) {
		t.Fatalf("items: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("GET", "/api/v1/menus/footer", "", ""); resp.StatusCode != 200 || !strings.Contains(out, `"url":"/b"`) {
		t.Fatalf("get by slug: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("GET", "/api/v1/menus/"+id, "", ""); resp.StatusCode != 200 || !strings.Contains(out, `"slug":"footer"`) {
		t.Fatalf("get by id: %d %s", resp.StatusCode, out)
	}
	// One parameter name per path position (OpenAPI 3.1 forbids {slug} beside {id}).
	for _, op := range s.app.Deps.API.Operations() {
		if strings.Contains(op.Path, "{slug}") {
			t.Errorf("%s %s: use {id}", op.Method, op.Path)
		}
	}
	if resp, out := s.do("POST", "/api/v1/menus", `{"name":""}`, "application/json"); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(out, "validation_failed") {
		t.Fatalf("invalid: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("GET", "/api/v1/menus", "", ""); resp.StatusCode != 200 || !strings.Contains(out, `"total":1`) {
		t.Fatalf("list: %d %s", resp.StatusCode, out)
	}
	// Copying menu A's items (with their IDs) into menu B must not collide.
	resp, out = s.do("POST", "/api/v1/menus", `{"name":"Copy"}`, "application/json")
	var envB struct {
		Data menus.Menu `json:"data"`
	}
	json.Unmarshal([]byte(out), &envB)
	_, out = s.do("GET", "/api/v1/menus/"+id, "", "")
	var envA struct {
		Data menus.Menu `json:"data"`
	}
	json.Unmarshal([]byte(out), &envA)
	itemsJSON, _ := json.Marshal(map[string]any{"items": envA.Data.Items})
	if resp, out := s.do("PUT", "/api/v1/menus/"+envB.Data.ID+"/items", string(itemsJSON), "application/json"); resp.StatusCode != 200 || !strings.Contains(out, `"label":"B"`) {
		t.Fatalf("copy items into B: %d %s", resp.StatusCode, out)
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	a, _ := svc.Get(context.Background(), "footer")
	b, _ := svc.Get(context.Background(), "copy")
	if a == nil || b == nil || len(a.Items) != 2 || len(b.Items) != 2 || a.Items[0].ID == b.Items[0].ID {
		t.Fatalf("menus after copy: %+v / %+v", a, b)
	}
	if resp, _ := s.do("DELETE", "/api/v1/menus/"+id, "", ""); resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/api/v1/menus/footer", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted: %d", resp.StatusCode)
	}
}
