package api_test

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
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

// fnModule is a throwaway module (unlisted names may sit anywhere in the
// module list).
type fnModule struct {
	name string
	reg  func(d *kernel.Deps) error
}

func (m fnModule) Name() string                  { return m.name }
func (fnModule) Migrations() fs.FS               { return nil }
func (m fnModule) Register(d *kernel.Deps) error { return m.reg(d) }

type itemIn struct {
	Title string `json:"title"`
}

// lateOps registers operations from a module booted AFTER api, proving the
// lazy mount sees them.
var lateOps = fnModule{name: "zzops", reg: func(d *kernel.Deps) error {
	d.API.Add(api.Op("GET", "/zz/echo/{id}", "Echo", []string{"zz"}, nil, nil, map[string]string{}, func(w http.ResponseWriter, r *http.Request) {
		api.WriteItem(w, map[string]string{"id": chi.URLParam(r, "id")})
	}))
	d.API.Add(api.Op("GET", "/v1/zz/secret", "Secret", []string{"zz"}, []string{"users.manage"}, nil, nil, func(w http.ResponseWriter, r *http.Request) {
		api.WriteItem(w, "secret")
	}))
	d.API.Add(api.Op("GET", "/zz/panic", "Panic", []string{"zz"}, nil, nil, nil, func(http.ResponseWriter, *http.Request) {
		panic("secret stack detail")
	}))
	d.API.Add(api.Op("POST", "/zz/items", "Create item", []string{"zz"}, []string{"content.create"}, itemIn{}, itemIn{}, func(w http.ResponseWriter, r *http.Request) {
		var in itemIn
		if err := api.Decode(w, r, &in); err != nil {
			api.Fail(w, r, err)
			return
		}
		api.WriteItem(w, in)
	}))
	return nil
}}

func bootAPI(t *testing.T, before ...kernel.Module) *kernel.App {
	t.Helper()
	return apitest.Boot(t, append(before, api.New(), lateOps)...)
}

func errCode(body map[string]any) string { return apitest.ErrorCode(body) }

func TestOpenAPIServedAnonymouslyWithLateOperations(t *testing.T) {
	app := bootAPI(t)
	anon := apitest.NewClient(t, app).Anonymous()
	w := anon.Do("GET", "/api/v1/openapi.json", nil, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("openapi.json: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
		Servers []struct{ URL string }    `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for p, m := range map[string]string{"/v1/zz/echo/{id}": "get", "/v1/zz/secret": "get", "/v1/zz/items": "post", "/v1/site": "get", "/v1/me": "get", "/v1/health": "get"} {
		if doc.Paths[p][m] == nil {
			t.Errorf("served document misses %s %s", m, p)
		}
	}
	documented := 0
	for _, item := range doc.Paths {
		documented += len(item)
	}
	if registered := len(app.Deps.API.Operations()); documented != registered || registered < 7 {
		t.Errorf("served document lists %d operations, registry holds %d", documented, registered)
	}
	if doc.Servers[0].URL != "http://localhost/api" {
		t.Errorf("server: %v", doc.Servers)
	}
	d, err := libopenapi.NewDocumentWithConfiguration(w.Body.Bytes(), &datamodel.DocumentConfiguration{IgnoreArrayCircularReferences: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.BuildV3Model(); err != nil {
		t.Fatalf("served document invalid: %v", err)
	}
	// site.base_url wins over the configured base URL... but the document
	// is built once, so a fresh app is needed to observe it.
	app2 := bootAPI(t)
	if err := app2.Deps.Settings.Set(context.Background(), "site.base_url", "https://cms.example.com/"); err != nil {
		t.Fatal(err)
	}
	w = apitest.NewClient(t, app2).Anonymous().Do("GET", "/api/v1/openapi.json", nil, nil)
	if !strings.Contains(w.Body.String(), `"url":"https://cms.example.com/api"`) {
		t.Fatalf("site.base_url not used: %.300s", w.Body.String())
	}
}

func TestDocsPage(t *testing.T) {
	app := bootAPI(t)
	anon := apitest.NewClient(t, app).Anonymous()
	w := anon.Do("GET", "/api/docs", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("docs: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{`dir="rtl"`, `src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.25.0" integrity="sha384-`, `crossorigin="anonymous"`, `data-url="/api/v1/openapi.json"`} {
		if !strings.Contains(body, want) {
			t.Errorf("docs page misses %s", want)
		}
	}
	if err := app.Deps.Settings.Set(context.Background(), "site.locale", "en"); err != nil {
		t.Fatal(err)
	}
	if w := anon.Do("GET", "/api/docs", nil, nil); !strings.Contains(w.Body.String(), `dir="ltr"`) {
		t.Fatal("docs page should follow site.locale")
	}
}

func TestScopeEnforced403(t *testing.T) {
	app := bootAPI(t)
	limited := apitest.NewClient(t, app, "content.view")
	status, body := limited.Get("/zz/secret")
	if status != 403 || errCode(body) != "forbidden" || body["ok"] != false {
		t.Fatalf("limited key: %d %v", status, body)
	}
	if status, _ := apitest.NewClient(t, app).Get("/zz/secret"); status != 200 {
		t.Fatalf("admin * key: %d", status)
	}
	// a key never exceeds its owner's role
	editorStar := apitest.New(t, app, apitest.Options{Role: "editor"})
	if status, _ := editorStar.Get("/zz/secret"); status != 403 {
		t.Fatalf("editor with * key: %d", status)
	}
	status, body = limited.Anonymous().Get("/zz/secret")
	if status != 401 || errCode(body) != "unauthorized" {
		t.Fatalf("anonymous: %d %v", status, body)
	}
	// scopes are enforced on writes too
	if status, body := limited.Post("/zz/items", itemIn{Title: "x"}); status != 403 {
		t.Fatalf("write without scope: %d %v", status, body)
	}
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func TestSessionAuthAlsoAccepted(t *testing.T) {
	app := bootAPI(t)
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "ed@example.com", "Ed", "correct horse battery", "editor"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, string) {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	_, page := get("/admin/login")
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no csrf on login page")
	}
	resp, err := c.PostForm(srv.URL+"/admin/login", url.Values{"_csrf": {m[1]}, "email": {"ed@example.com"}, "password": {"correct horse battery"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	status, body := get("/api/v1/me")
	var me struct {
		OK   bool `json:"ok"`
		Data struct {
			Kind   string                       `json:"kind"`
			User   struct{ Email, Role string } `json:"user"`
			APIKey any                          `json:"api_key"`
			Scopes []string                     `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil || status != 200 {
		t.Fatalf("me via session: %d %s", status, body)
	}
	if me.Data.Kind != "user" || me.Data.User.Role != "editor" || me.Data.APIKey != nil || !contains(me.Data.Scopes, "content.edit") || contains(me.Data.Scopes, "users.manage") {
		t.Fatalf("me: %+v", me.Data)
	}
	if status, body := get("/api/v1/zz/echo/abc"); status != 200 || !strings.Contains(body, `"id":"abc"`) {
		t.Fatalf("op via session: %d %s", status, body)
	}
	if status, _ := get("/api/v1/zz/secret"); status != 403 {
		t.Fatalf("session user without permission: %d", status)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestRateLimit429(t *testing.T) {
	app := bootAPI(t)
	c := apitest.New(t, app, apitest.Options{RatePerMin: 2})
	for i := 0; i < 2; i++ {
		if status, body := c.Get("/zz/echo/1"); status != 200 {
			t.Fatalf("request %d: %d %v", i+1, status, body)
		}
	}
	w := c.Do("GET", "/zz/echo/1", nil, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("third request: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
}

func TestEnvelopeOnPanic(t *testing.T) {
	app := bootAPI(t)
	w := apitest.NewClient(t, app).Do("GET", "/zz/panic", nil, nil)
	body := w.Body.String()
	if w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("panic: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.OK || env.Error.Code != "internal_error" {
		t.Fatalf("envelope: %s", body)
	}
	for _, leak := range []string{"secret stack detail", "goroutine", ".go:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q: %s", leak, body)
		}
	}
	// the server keeps serving
	if status, _ := apitest.NewClient(t, app).Get("/zz/echo/2"); status != 200 {
		t.Fatalf("after panic: %d", status)
	}
}

func TestRoutingEnvelopes(t *testing.T) {
	app := bootAPI(t)
	c := apitest.NewClient(t, app)
	if status, body := c.Get("/zz/echo/a1"); status != 200 || apitest.Data(body)["id"] != "a1" {
		t.Fatalf("path param: %d %v", status, body)
	}
	if status, body := c.Get("/nope"); status != 404 || errCode(body) != "not_found" {
		t.Fatalf("unknown path: %d %v", status, body)
	}
	w := c.Do("DELETE", "/zz/echo/a1", nil, nil)
	if w.Code != 405 || !strings.Contains(w.Body.String(), `"code":"method_not_allowed"`) || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("wrong method: %d Allow=%q %s", w.Code, w.Header().Get("Allow"), w.Body)
	}
	if w := c.Do("PUT", "/zz/items", nil, nil); w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatalf("PUT on POST op: %d Allow=%q", w.Code, w.Header().Get("Allow"))
	}
	if w := c.Do("HEAD", "/zz/echo/a1", nil, nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD on GET op: %d %q", w.Code, w.Body)
	}
	if status, body := c.Post("/zz/items", `{"title":"x","bogus":1}`); status != 400 || errCode(body) != "invalid_json" {
		t.Fatalf("unknown field: %d %v", status, body)
	} else if f, _ := body["error"].(map[string]any)["fields"].(map[string]any); f["bogus"] == nil {
		t.Fatalf("field hint: %v", body)
	}
	if status, body := c.Post("/zz/items", itemIn{Title: "ok"}); status != 200 || apitest.Data(body)["title"] != "ok" {
		t.Fatalf("create: %d %v", status, body)
	}
}

func TestMeWithAPIKey(t *testing.T) {
	app := bootAPI(t)
	c := apitest.NewClient(t, app, "content.view", "media.upload")
	status, body := c.Get("/me")
	d := apitest.Data(body)
	if status != 200 || d["kind"] != "api_key" {
		t.Fatalf("me: %d %v", status, body)
	}
	if k, _ := d["api_key"].(map[string]any); k["id"] != c.Key.ID {
		t.Fatalf("api key: %v", d["api_key"])
	}
	scopes, _ := d["scopes"].([]any)
	if len(scopes) != 2 {
		t.Fatalf("scopes: %v", scopes)
	}
	if status, body := c.Get("/health"); status != 200 || apitest.Data(body)["database"] != "ok" {
		t.Fatalf("health: %d %v", status, body)
	}
}

// fake services published by a module booted before api.
type fakeContent struct{ content.Service }

func (fakeContent) Types(context.Context) ([]content.Type, error) {
	return []content.Type{{Slug: "post"}, {Slug: "page"}}, nil
}

type fakeTax struct{ taxonomy.Service }

func (fakeTax) Taxonomies(context.Context) ([]taxonomy.Taxonomy, error) {
	return []taxonomy.Taxonomy{{Slug: "category"}}, nil
}

type fakeMenus struct{ menus.Service }

func (fakeMenus) List(context.Context) ([]menus.Menu, error) {
	return []menus.Menu{{Slug: "main", Location: "primary"}}, nil
}

type fakeTheme struct{ theme.Service }

func (fakeTheme) Active(context.Context) theme.Info {
	return theme.Info{Slug: "example", Name: "Example"}
}
func (fakeTheme) Templates(context.Context) ([]theme.TemplateFile, error) {
	return []theme.TemplateFile{{Name: "index.html", Source: "theme"}}, nil
}

func TestSiteStructureOp(t *testing.T) {
	// No content/taxonomy/menus/theme modules: every list is [] (not null).
	app := bootAPI(t)
	w := apitest.NewClient(t, app).Do("GET", "/site", nil, nil)
	if w.Code != 200 {
		t.Fatalf("site: %d %s", w.Code, w.Body)
	}
	for _, want := range []string{`"types":[]`, `"taxonomies":[]`, `"menus":[]`, `"templates":[]`, `"theme":null`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("empty site misses %s: %s", want, w.Body)
		}
	}
	if status, _ := apitest.NewClient(t, app, "media.upload").Get("/site"); status != 403 {
		t.Fatalf("site without content.view: %d", status)
	}

	services := fnModule{name: "fakesvc", reg: func(d *kernel.Deps) error {
		d.Services.Set(kernel.ServiceContent, content.Service(fakeContent{}))
		d.Services.Set(kernel.ServiceTaxonomy, taxonomy.Service(fakeTax{}))
		d.Services.Set(kernel.ServiceMenus, menus.Service(fakeMenus{}))
		d.Services.Set(kernel.ServiceTheme, theme.Service(fakeTheme{}))
		return nil
	}}
	app = bootAPI(t, services)
	status, body := apitest.NewClient(t, app).Get("/site")
	d := apitest.Data(body)
	if status != 200 {
		t.Fatalf("site: %d %v", status, body)
	}
	if n := len(d["types"].([]any)); n != 2 {
		t.Errorf("types: %v", d["types"])
	}
	if d["taxonomies"].([]any)[0].(map[string]any)["slug"] != "category" ||
		d["menus"].([]any)[0].(map[string]any)["location"] != "primary" ||
		d["theme"].(map[string]any)["slug"] != "example" ||
		d["templates"].([]any)[0].(map[string]any)["name"] != "index.html" {
		t.Errorf("site structure: %v", d)
	}
	if s, _ := d["site"].(map[string]any); s["locale"] != "ar" || s["dir"] != "rtl" {
		t.Errorf("site info: %v", d["site"])
	}
}

func TestDuplicateAfterNormalizationMergesScopes(t *testing.T) {
	dup := fnModule{name: "zzdup", reg: func(d *kernel.Deps) error {
		d.API.Add(kernel.APIOperation{Method: "GET", Path: "/zz/dup", Scopes: []string{"content.view"},
			Handler: func(w http.ResponseWriter, _ *http.Request) { api.WriteItem(w, "first") }})
		d.API.Add(kernel.APIOperation{Method: "get", Path: "/v1/zz/dup", Scopes: []string{"users.manage", "content.view"},
			Handler: func(w http.ResponseWriter, _ *http.Request) { api.WriteItem(w, "second") }})
		return nil
	}}
	app := apitest.Boot(t, api.New(), dup)
	if status, body := apitest.NewClient(t, app, "content.view").Get("/zz/dup"); status != 403 {
		t.Fatalf("duplicate must require the union of scopes: %d %v", status, body)
	}
	c := apitest.NewClient(t, app)
	if status, body := c.Get("/zz/dup"); status != 200 || body["data"] != "first" {
		t.Fatalf("first handler must be mounted: %d %v", status, body)
	}
	w := c.Anonymous().Do("GET", "/api/v1/openapi.json", nil, nil)
	var doc struct {
		Paths map[string]map[string]struct {
			Scopes []string `json:"x-scopes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Paths["/v1/zz/dup"]["get"].Scopes; strings.Join(got, ",") != "content.view,users.manage" {
		t.Fatalf("documented scopes: %v", got)
	}
}
