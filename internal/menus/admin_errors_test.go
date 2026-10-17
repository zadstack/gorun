package menus_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// bootFull is boot with the content and taxonomy services available, so the
// editor's search panel and item resolution have real sources.
func bootFull(t *testing.T, role string) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(), menus.New()})
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
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := us.Create(context.Background(), "u@example.com", "U", "correct horse battery", role); err != nil {
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

func (s *site) exec(q string) {
	s.t.Helper()
	if _, err := s.app.Deps.DB.Exec(q); err != nil {
		s.t.Fatalf("%s: %v", q, err)
	}
}

func (s *site) menuSvc() menus.Service {
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	return svc
}

func TestAdminMenuSearchSources(t *testing.T) {
	s := bootFull(t, "editor")
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	ts, _ := kernel.Get[taxonomy.Service](s.app.Deps, kernel.ServiceTaxonomy)
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "Zebra crossing", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "Zebra draft"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Name: "Zoology"}); err != nil {
		t.Fatal(err)
	}
	post, err := cs.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	post.HasArchive, post.Public = true, true
	if err := cs.SaveType(ctx, *post); err != nil {
		t.Fatal(err)
	}

	resp, page := s.do("GET", "/admin/x/menus/search?kind=content&q=Zebra", "", "")
	if resp.StatusCode != 200 || !strings.Contains(page, "Zebra crossing") || strings.Contains(page, "Zebra draft") {
		t.Fatalf("content search (published only): %d\n%s", resp.StatusCode, page)
	}
	resp, page = s.do("GET", "/admin/x/menus/search?kind=term&q=Zoo", "", "")
	if resp.StatusCode != 200 || !strings.Contains(page, "Zoology") || !strings.Contains(page, "/category/zoology") {
		t.Fatalf("term search: %d\n%s", resp.StatusCode, page)
	}
	resp, page = s.do("GET", "/admin/x/menus/search?kind=archive", "", "")
	if resp.StatusCode != 200 || !strings.Contains(page, archiveHint(post)) {
		t.Fatalf("archive search: %d\n%s", resp.StatusCode, page)
	}
	if _, page = s.do("GET", "/admin/x/menus/search?kind=archive&q=no-such-archive", "", ""); strings.Contains(page, archiveHint(post)) {
		t.Fatalf("archive search ignores q:\n%s", page)
	}

	// Failing sources render an empty (not "unavailable") panel.
	s.exec(`ALTER TABLE content_terms RENAME TO content_terms_gone`)
	if resp, page := s.do("GET", "/admin/x/menus/search?kind=term&q=Zoo", "", ""); resp.StatusCode != 200 || strings.Contains(page, "Zoology") {
		t.Fatalf("term search with failing terms: %d", resp.StatusCode)
	}
	s.exec(`ALTER TABLE content_terms_gone RENAME TO content_terms`)
	s.exec(`ALTER TABLE taxonomies RENAME TO taxonomies_gone`)
	if resp, page := s.do("GET", "/admin/x/menus/search?kind=term&q=Zoo", "", ""); resp.StatusCode != 200 || strings.Contains(page, "Zoology") {
		t.Fatalf("term search with failing taxonomies: %d", resp.StatusCode)
	}
	s.exec(`ALTER TABLE content RENAME TO content_gone`)
	if resp, page := s.do("GET", "/admin/x/menus/search?kind=content&q=Zebra", "", ""); resp.StatusCode != 200 || strings.Contains(page, "Zebra crossing") {
		t.Fatalf("content search with failing store: %d", resp.StatusCode)
	}
	s.exec(`ALTER TABLE content_types RENAME TO content_types_gone`)
	if resp, page := s.do("GET", "/admin/x/menus/search?kind=archive", "", ""); resp.StatusCode != 200 || strings.Contains(page, archiveHint(post)) {
		t.Fatalf("archive search with failing types: %d", resp.StatusCode)
	}
}

// archiveHint is the hint the archive search shows for t.
func archiveHint(t *content.Type) string { return menus.ArchivePath(t) }

func TestAdminMenuFormErrors(t *testing.T) {
	s := boot(t, "editor")
	ctx := context.Background()
	resp, _ := s.form("/admin/menus", url.Values{"name": {"Main"}})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	nested := `[{"label":"Top","url":"/","children":[{"label":"Sub","url":"/sub"}]}]`
	if resp, _ := s.form(loc+"/items", url.Values{"items": {nested}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("items: %d", resp.StatusCode)
	}
	// The list counts nested items too.
	if resp, page := s.do("GET", "/admin/menus", "", ""); resp.StatusCode != 200 || !strings.Contains(page, "Main") {
		t.Fatalf("list: %d", resp.StatusCode)
	}

	if resp, _ := s.do("POST", "/admin/menus", `{bad json`, "application/json"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create bad body: %d", resp.StatusCode)
	}
	if resp, _ := s.do("POST", loc, `{bad json`, "application/json"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update bad body: %d", resp.StatusCode)
	}
	if resp, page := s.form(loc, url.Values{"name": {""}, "slug": {"main"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("update invalid: %d", resp.StatusCode)
	}
	if resp, page := s.form(loc+"/items", url.Values{"items": {"not json"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, `role="alert"`) {
		t.Fatalf("items not json: %d", resp.StatusCode)
	}
	m, _ := s.menuSvc().Get(ctx, "main")
	if m == nil || m.Name != "Main" || len(m.Items) != 1 || len(m.Items[0].Children) != 1 {
		t.Fatalf("rejected forms changed the menu: %+v", m)
	}
	// "null" clears the tree.
	if resp, _ := s.form(loc+"/items", url.Values{"items": {"null"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("null items: %d", resp.StatusCode)
	}
	if m, _ := s.menuSvc().Get(ctx, "main"); m == nil || len(m.Items) != 0 {
		t.Fatalf("null items kept the tree: %+v", m)
	}

	for _, path := range []string{"/admin/menus/missing", "/admin/menus/missing/items", "/admin/menus/missing/delete"} {
		if resp, _ := s.form(path, url.Values{"name": {"x"}, "items": {"[]"}}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestAdminMenuStoreFailuresAre500(t *testing.T) {
	s := boot(t, "editor")
	resp, _ := s.form("/admin/menus", url.Values{"name": {"Main"}})
	loc := resp.Header.Get("Location")
	trigger := func(name, event string) {
		s.exec(`CREATE TRIGGER ` + name + ` BEFORE ` + event + ` BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	}

	trigger("no_menu_insert", "INSERT ON menus")
	if resp, _ := s.form("/admin/menus", url.Values{"name": {"Other"}}); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create failure: %d", resp.StatusCode)
	}
	trigger("no_menu_update", "UPDATE ON menus")
	if resp, _ := s.form(loc, url.Values{"name": {"Renamed"}, "slug": {"main"}}); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("update failure: %d", resp.StatusCode)
	}
	trigger("no_item_insert", "INSERT ON menu_items")
	if resp, _ := s.form(loc+"/items", url.Values{"items": {`[{"label":"A","url":"/a"}]`}}); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("items failure: %d", resp.StatusCode)
	}
	if m, _ := s.menuSvc().Get(context.Background(), "main"); m == nil || m.Name != "Main" || len(m.Items) != 0 {
		t.Fatalf("failed writes changed the menu: %+v", m)
	}

	s.exec(`ALTER TABLE menus RENAME TO menus_gone`)
	if resp, _ := s.do("GET", "/admin/menus", "", ""); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("list failure: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", loc, "", ""); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("edit failure: %d", resp.StatusCode)
	}
	if resp, out := s.do("GET", "/api/v1/menus", "", ""); resp.StatusCode != http.StatusInternalServerError || !strings.Contains(out, `"internal"`) {
		t.Fatalf("api list failure: %d %s", resp.StatusCode, out)
	}
}

func TestMenusAPIForbiddenWithoutPermission(t *testing.T) {
	s := boot(t, "author")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/menus", ""},
		{"GET", "/api/v1/menus/main", ""},
		{"POST", "/api/v1/menus", `{"name":"X"}`},
		{"PUT", "/api/v1/menus/x/items", `{"items":[]}`},
		{"DELETE", "/api/v1/menus/x", ""},
	} {
		if resp, out := s.do(c.method, c.path, c.body, "application/json"); resp.StatusCode != http.StatusForbidden || !strings.Contains(out, "forbidden") {
			t.Errorf("%s %s: %d %s", c.method, c.path, resp.StatusCode, out)
		}
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	if all, _ := svc.List(context.Background()); len(all) != 0 {
		t.Fatalf("forbidden create stored a menu: %+v", all)
	}
}

func TestMenusAPIErrors(t *testing.T) {
	s := boot(t, "admin")
	if resp, out := s.do("GET", "/api/v1/menus", "", ""); resp.StatusCode != 200 || !strings.Contains(out, `"data":[]`) {
		t.Fatalf("empty list must be []: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("POST", "/api/v1/menus", `{`, "application/json"); resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, "bad_request") {
		t.Fatalf("save bad json: %d %s", resp.StatusCode, out)
	}
	if resp, _ := s.do("PUT", "/api/v1/menus/x/items", `{`, "application/json"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("items bad json: %d", resp.StatusCode)
	}
	if resp, out := s.do("PUT", "/api/v1/menus/missing/items", `{"items":[]}`, "application/json"); resp.StatusCode != http.StatusNotFound || !strings.Contains(out, "not_found") {
		t.Fatalf("items of a missing menu: %d %s", resp.StatusCode, out)
	}
	if resp, _ := s.do("DELETE", "/api/v1/menus/missing", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing: %d", resp.StatusCode)
	}
}
