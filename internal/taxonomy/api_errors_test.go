package taxonomy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type apiClient struct {
	r    chi.Router
	user *auth.User
}

func newAPIClient(t *testing.T, app *kernel.App) *apiClient {
	t.Helper()
	c := &apiClient{r: chi.NewRouter()}
	c.r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), c.user)))
		})
	})
	for _, op := range app.Deps.API.Operations() {
		c.r.Method(op.Method, op.Path, op.Handler)
	}
	return c
}

// raw sends body verbatim (so malformed JSON can be tested).
func (c *apiClient) raw(method, path, body string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var env map[string]any
	json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

func (c *apiClient) call(method, path string, body any) (int, map[string]any) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	return c.raw(method, path, buf.String())
}

func errCode(env map[string]any) string {
	e, _ := env["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestAPIRejectsAnonymousOnEveryOperation(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app)
	_, ts := services(t, app)
	cat := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News"})
	for _, op := range []struct{ method, path string }{
		{"GET", "/v1/taxonomies"},
		{"POST", "/v1/taxonomies"},
		{"GET", "/v1/taxonomies/category"},
		{"PATCH", "/v1/taxonomies/category"},
		{"DELETE", "/v1/taxonomies/category"},
		{"GET", "/v1/taxonomies/category/terms"},
		{"POST", "/v1/taxonomies/category/terms"},
		{"GET", "/v1/taxonomies/category/terms/" + cat.ID},
		{"PATCH", "/v1/taxonomies/category/terms/" + cat.ID},
		{"DELETE", "/v1/taxonomies/category/terms/" + cat.ID},
	} {
		code, env := c.raw(op.method, op.path, `{}`)
		if code != http.StatusForbidden || errCode(env) != "forbidden" {
			t.Errorf("%s %s anonymous: %d %v", op.method, op.path, code, env)
		}
	}
	if _, err := ts.GetTerm(t.Context(), cat.ID); err != nil {
		t.Fatalf("forbidden delete removed the term: %v", err)
	}
}

func TestAPITaxonomyCRUDAndErrors(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app)
	c.user, _ = servicesAuth(t, app)
	_, ts := services(t, app)

	if code, env := c.call("GET", "/v1/taxonomies/category", nil); code != 200 || env["data"].(map[string]any)["slug"] != "category" {
		t.Fatalf("get: %d %v", code, env)
	}
	if code, env := c.call("GET", "/v1/taxonomies/nope", nil); code != 404 || errCode(env) != "not_found" {
		t.Fatalf("get missing: %d %v", code, env)
	}
	if code, env := c.raw("POST", "/v1/taxonomies", `{not json`); code != 400 || errCode(env) != "bad_request" {
		t.Fatalf("create bad json: %d %v", code, env)
	}
	code, env := c.call("POST", "/v1/taxonomies", map[string]any{"slug": "genre", "name": map[string]string{"en": "Genres"}, "types": []string{"post"}})
	if code != 201 || env["data"].(map[string]any)["slug"] != "genre" {
		t.Fatalf("create: %d %v", code, env)
	}
	if code, env := c.call("POST", "/v1/taxonomies", map[string]any{"slug": "Bad Slug", "types": []string{"nope"}}); code != 422 || errCode(env) != "validation_failed" {
		t.Fatalf("create invalid: %d %v", code, env)
	} else if f := env["error"].(map[string]any)["fields"].(map[string]any); f["slug"] == nil || f["name"] == nil || f["types"] == nil {
		t.Fatalf("validation fields: %v", f)
	}

	// PATCH keeps the slug from the path and replaces only sent keys.
	code, env = c.call("PATCH", "/v1/taxonomies/genre", map[string]any{"slug": "ignored", "hierarchical": true})
	if code != 200 || env["data"].(map[string]any)["slug"] != "genre" || env["data"].(map[string]any)["hierarchical"] != true {
		t.Fatalf("patch: %d %v", code, env)
	}
	if g, _ := ts.Get(t.Context(), "genre"); g.Name["en"] != "Genres" || !g.Hierarchical {
		t.Fatalf("patch stored: %+v", g)
	}
	if code, _ := c.call("PATCH", "/v1/taxonomies/nope", map[string]any{}); code != 404 {
		t.Fatalf("patch missing: %d", code)
	}
	if code, _ := c.raw("PATCH", "/v1/taxonomies/genre", `[1,2]`); code != 400 {
		t.Fatalf("patch non-object: %d", code)
	}
	if code, env := c.raw("PATCH", "/v1/taxonomies/genre", `{"hierarchical":"yes"}`); code != 400 || errCode(env) != "bad_request" {
		t.Fatalf("patch wrong type: %d %v", code, env)
	}
	if code, env := c.call("PATCH", "/v1/taxonomies/genre", map[string]any{"name": map[string]string{}}); code != 422 {
		t.Fatalf("patch invalid: %d %v", code, env)
	}

	if code, _ := c.call("DELETE", "/v1/taxonomies/nope", nil); code != 404 {
		t.Fatalf("delete missing: %d", code)
	}
	if code, env := c.call("DELETE", "/v1/taxonomies/genre", nil); code != 200 || env["data"].(map[string]any)["deleted"] != true {
		t.Fatalf("delete: %d %v", code, env)
	}
	if _, err := ts.Get(t.Context(), "genre"); err != ErrNotFound {
		t.Fatalf("genre still stored: %v", err)
	}
}

func TestAPITermErrors(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app)
	c.user, _ = servicesAuth(t, app)
	_, ts := services(t, app)
	parent := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Parent"})
	for i := 0; i < 3; i++ {
		mustTerm(t, ts, Term{Taxonomy: "category", Name: "Child", ParentID: parent.ID})
	}

	if code, _ := c.call("GET", "/v1/taxonomies/nope/terms", nil); code != 404 {
		t.Fatalf("terms of a missing taxonomy: %d", code)
	}
	code, env := c.call("GET", "/v1/taxonomies/category/terms?parent_id="+parent.ID+"&per_page=2&page=2", nil)
	meta, _ := env["meta"].(map[string]any)
	if code != 200 || meta["total"] != 3.0 || meta["page"] != 2.0 || meta["per_page"] != 2.0 || len(env["data"].([]any)) != 1 {
		t.Fatalf("paged children: %d %v", code, env)
	}
	if _, env := c.call("GET", "/v1/taxonomies/category/terms?per_page=9999", nil); env["meta"].(map[string]any)["per_page"] != 500.0 {
		t.Fatalf("per_page capped: %v", env["meta"])
	}
	if code, env := c.call("GET", "/v1/taxonomies/category/terms/"+parent.ID, nil); code != 200 || env["data"].(map[string]any)["name"] != "Parent" {
		t.Fatalf("get term: %d %v", code, env)
	}
	if code, _ := c.call("GET", "/v1/taxonomies/category/terms/missing", nil); code != 404 {
		t.Fatalf("missing term: %d", code)
	}
	if code, _ := c.raw("POST", "/v1/taxonomies/category/terms", `nope`); code != 400 {
		t.Fatalf("create term bad json: %d", code)
	}
	if code, env := c.call("POST", "/v1/taxonomies/tag/terms", map[string]any{"name": "x", "parent_id": parent.ID}); code != 422 || env["error"].(map[string]any)["fields"].(map[string]any)["parent_id"] == nil {
		t.Fatalf("flat taxonomy parent: %d %v", code, env)
	}

	if code, _ := c.call("PATCH", "/v1/taxonomies/tag/terms/"+parent.ID, map[string]any{"name": "x"}); code != 404 {
		t.Fatalf("patch in the wrong taxonomy: %d", code)
	}
	if code, _ := c.raw("PATCH", "/v1/taxonomies/category/terms/"+parent.ID, `{`); code != 400 {
		t.Fatalf("patch term bad json: %d", code)
	}
	if code, _ := c.raw("PATCH", "/v1/taxonomies/category/terms/"+parent.ID, `{"meta":"flat"}`); code != 400 {
		t.Fatalf("patch term wrong type: %d", code)
	}
	if code, env := c.call("PATCH", "/v1/taxonomies/category/terms/"+parent.ID, map[string]any{"name": "  "}); code != 422 || env["error"].(map[string]any)["fields"].(map[string]any)["name"] == nil {
		t.Fatalf("patch term empty name: %d %v", code, env)
	}
	if got, _ := ts.GetTerm(t.Context(), parent.ID); got.Name != "Parent" {
		t.Fatalf("invalid patch changed the term: %+v", got)
	}

	if code, _ := c.call("DELETE", "/v1/taxonomies/category/terms/missing", nil); code != 404 {
		t.Fatalf("delete missing term: %d", code)
	}
}

func TestAPIInternalErrorsAre500(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app)
	c.user, _ = servicesAuth(t, app)
	_, ts := services(t, app)
	term := mustTerm(t, ts, Term{Taxonomy: "tag", Name: "go"})
	raiseOn(t, ts, "no_term_delete", "DELETE ON terms")
	if code, env := c.call("DELETE", "/v1/taxonomies/tag/terms/"+term.ID, nil); code != 500 || errCode(env) != "internal" {
		t.Fatalf("delete term with a failing store: %d %v", code, env)
	}
	if _, err := ts.GetTerm(t.Context(), term.ID); err != nil {
		t.Fatalf("term gone after a failed delete: %v", err)
	}
	if _, err := app.Deps.DB.Exec(`ALTER TABLE taxonomies RENAME TO taxonomies_gone`); err != nil {
		t.Fatal(err)
	}
	code, env := c.call("GET", "/v1/taxonomies", nil)
	if code != 500 || errCode(env) != "internal" {
		t.Fatalf("list with a broken store: %d %v", code, env)
	}
	if msg, _ := env["error"].(map[string]any)["message"].(string); strings.Contains(msg, "taxonomies") {
		t.Fatalf("internal error leaks details: %q", msg)
	}
}
