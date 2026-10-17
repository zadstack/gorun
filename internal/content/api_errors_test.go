package content_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// rawDo sends a raw (possibly malformed) body through the API router.
func (c *apiClient) rawDo(method, path, body string) int {
	c.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)
	return rec.Code
}

// Every content API handler re-checks the caller's permission itself: a
// customer (no content permissions) gets a 403 forbidden envelope everywhere.
func TestAPIHandlersRejectCallersWithoutPermission(t *testing.T) {
	app := bootApp(t)
	it, err := contentService(t, app).Create(context.Background(), content.Input{Type: "post", Title: "P"})
	if err != nil {
		t.Fatal(err)
	}
	cust := mustUser(t, app, "cust@example.com", "customer")
	c := newAPIClient(t, app, cust)
	cases := []struct{ method, path string }{
		{"GET", "/v1/types"},
		{"POST", "/v1/types"},
		{"GET", "/v1/types/post"},
		{"PATCH", "/v1/types/post"},
		{"DELETE", "/v1/types/post"},
		{"GET", "/v1/content"},
		{"POST", "/v1/content/query"},
		{"POST", "/v1/content"},
		{"GET", "/v1/content/" + it.ID},
		{"PATCH", "/v1/content/" + it.ID},
		{"DELETE", "/v1/content/" + it.ID},
		{"POST", "/v1/content/" + it.ID + "/publish"},
		{"POST", "/v1/content/" + it.ID + "/unpublish"},
		{"GET", "/v1/content/" + it.ID + "/revisions"},
		{"POST", "/v1/content/" + it.ID + "/revisions/x/restore"},
		{"GET", "/v1/comments"},
		{"PATCH", "/v1/comments/x"},
		{"DELETE", "/v1/comments/x"},
		{"GET", "/v1/content/" + it.ID + "/translations"},
		{"POST", "/v1/content/" + it.ID + "/translations"},
		{"POST", "/v1/content/" + it.ID + "/translations/link"},
		{"DELETE", "/v1/content/" + it.ID + "/translations"},
	}
	for _, tc := range cases {
		code, env := c.do(tc.method, tc.path, map[string]any{})
		if code != http.StatusForbidden || env.OK || env.Error.Code != "forbidden" {
			t.Errorf("%s %s: %d %+v", tc.method, tc.path, code, env)
		}
	}
	if got, _ := contentService(t, app).Get(context.Background(), it.ID); got == nil || got.Status == "trash" {
		t.Fatal("a forbidden DELETE must leave the item alone")
	}
}

// Malformed JSON bodies answer 400 bad_request before any change is made.
func TestAPIRejectsMalformedJSONBodies(t *testing.T) {
	app := bootApp(t)
	admin := mustUser(t, app, "admin@example.com", "admin")
	cs := contentService(t, app)
	it, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "Keep"})
	if err != nil {
		t.Fatal(err)
	}
	c := newAPIClient(t, app, admin)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/types"},
		{"PATCH", "/v1/types/post"},
		{"POST", "/v1/content/query"},
		{"POST", "/v1/content"},
		{"PATCH", "/v1/content/" + it.ID},
		{"POST", "/v1/content/" + it.ID + "/translations"},
		{"POST", "/v1/content/" + it.ID + "/translations/link"},
		{"PATCH", "/v1/comments/x"},
	} {
		if code := c.rawDo(tc.method, tc.path, "{not json"); code != http.StatusBadRequest {
			t.Errorf("%s %s: %d", tc.method, tc.path, code)
		}
	}
	// A PATCH body whose keys have the wrong JSON type fails the overlay.
	if code := c.rawDo("PATCH", "/v1/types/post", `{"fields":"not a list"}`); code != http.StatusBadRequest {
		t.Errorf("patch type with mistyped fields: %d", code)
	}
	if got, _ := cs.Get(context.Background(), it.ID); got.Title != "Keep" {
		t.Fatalf("item changed by a rejected request: %q", got.Title)
	}
}

// Service errors map to their envelopes: unknown ids 404, system types 409,
// schema validation 422.
func TestAPIMapsServiceErrorsToEnvelopes(t *testing.T) {
	app := bootApp(t)
	admin := mustUser(t, app, "admin@example.com", "admin")
	c := newAPIClient(t, app, admin)
	notFound := []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/types/nope", nil},
		{"PATCH", "/v1/types/nope", map[string]any{}},
		{"GET", "/v1/content/nope", nil},
		{"PATCH", "/v1/content/nope", map[string]any{}},
		{"DELETE", "/v1/content/nope", nil},
		{"POST", "/v1/content/nope/publish", nil},
		{"POST", "/v1/content/nope/unpublish", nil},
		{"POST", "/v1/content/nope/revisions/nope/restore", nil},
		{"GET", "/v1/content/nope/translations", nil},
		{"POST", "/v1/content/nope/translations", map[string]any{"locale": "ar"}},
		{"POST", "/v1/content/nope/translations/link", map[string]any{"id": "other"}},
		{"DELETE", "/v1/content/nope/translations", nil},
		{"PATCH", "/v1/comments/nope", map[string]any{"status": "approved"}},
		{"DELETE", "/v1/comments/nope", nil},
	}
	for _, tc := range notFound {
		code, env := c.do(tc.method, tc.path, tc.body)
		if code != http.StatusNotFound || env.Error.Code != "not_found" {
			t.Errorf("%s %s: %d %+v", tc.method, tc.path, code, env)
		}
	}

	code, env := c.do("DELETE", "/v1/types/post", nil)
	if code != http.StatusConflict || env.Error.Code != "conflict" {
		t.Fatalf("delete system type: %d %+v", code, env)
	}
	code, env = c.do("POST", "/v1/types", map[string]any{"slug": "Bad Slug!", "name": map[string]string{"en": "Bad"}})
	if code != http.StatusUnprocessableEntity || env.Error.Code != "validation_failed" || len(env.Error.Fields) == 0 {
		t.Fatalf("invalid type: %d %+v", code, env)
	}
	if _, err := contentService(t, app).GetType(context.Background(), "Bad Slug!"); err == nil {
		t.Fatal("an invalid type was saved")
	}
	code, env = c.do("GET", "/v1/comments?status=bogus", nil)
	if code != http.StatusUnprocessableEntity || env.Error.Code != "validation_failed" {
		t.Fatalf("comments with an unknown status: %d %+v", code, env)
	}
}

// Restoring a revision through the API returns the restored item.
func TestAPIRestoreRevisionAndListRevisions(t *testing.T) {
	app := bootApp(t)
	admin := mustUser(t, app, "admin@example.com", "admin")
	c := newAPIClient(t, app, admin)
	cs := contentService(t, app)
	ctx := context.Background()
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "v1"})
	in := content.InputFromItem(it)
	in.Title = "v2"
	if _, err := cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	revs, err := cs.Revisions(ctx, it.ID)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v %d", err, len(revs))
	}
	code, env := c.do("GET", "/v1/content/"+it.ID+"/revisions", nil)
	if code != 200 || env.Meta["total"] != len(revs) {
		t.Fatalf("list revisions: %d %+v", code, env)
	}
	code, env = c.do("POST", "/v1/content/"+it.ID+"/revisions/"+revs[len(revs)-1].ID+"/restore", nil)
	if got, _ := cs.Get(ctx, it.ID); code != 200 || !env.OK || got.Title != "v1" {
		t.Fatalf("restore: %d %+v %q", code, env, got.Title)
	}
}

// QueryFromValues parses taxonomy and field filters from the query string.
func TestQueryFromValuesParsesFilters(t *testing.T) {
	v := url.Values{
		"type": {"product"}, "include_trash": {"1"}, "page": {"2"}, "per_page": {"5"},
		"order":            {"price:desc, ,title"},
		"taxonomy.tag":     {"a,b", "c"},
		"fields.price":     {"10"},
		"fields.sku.in":    {"x, y,"},
		"fields.name.like": {"bo"},
	}
	q := content.QueryFromValues(v)
	if q.Type != "product" || !q.IncludeTrash || q.Page != 2 || q.PerPage != 5 || len(q.Order) != 2 ||
		q.Order[0] != (content.Order{By: "price", Dir: "desc"}) || q.Order[1].By != "title" {
		t.Fatalf("query: %+v", q)
	}
	if tags := q.Taxonomy["tag"]; len(tags) != 3 || tags[2] != "c" {
		t.Fatalf("taxonomy: %+v", q.Taxonomy)
	}
	if f := q.Fields["price"]; f.Op != "eq" || f.Value != "10" {
		t.Fatalf("eq filter: %+v", f)
	}
	if f := q.Fields["sku"]; f.Op != "in" || len(f.Value.([]string)) != 2 {
		t.Fatalf("in filter: %+v", f)
	}
	if f := q.Fields["name"]; f.Op != "like" || f.Value != "bo" {
		t.Fatalf("like filter: %+v", f)
	}
}

// The list endpoint validates filters against the type schema and applies
// valid ones: unknown types, statuses, non-indexed fields, operators and
// uncoercible values answer 422; valid filters narrow the result.
func TestAPIContentQueryFilterValidation(t *testing.T) {
	app := bootApp(t)
	admin := mustUser(t, app, "admin@example.com", "admin")
	c := newAPIClient(t, app, admin)
	cs := contentService(t, app)
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "gadget", Name: map[string]string{"en": "Gadgets"}, Public: true, Supports: []string{"title", "parent"},
		Hierarchical: true, Fields: []content.Field{
			{Key: "price", Type: "number", Indexed: true},
			{Key: "live", Type: "boolean", Indexed: true},
			{Key: "at", Type: "datetime", Indexed: true},
			{Key: "sku", Type: "text", Indexed: true},
			{Key: "notes", Type: "text"},
		}}); err != nil {
		t.Fatal(err)
	}
	parent, err := cs.Create(ctx, content.Input{Type: "gadget", Title: "Parent", Fields: map[string]any{"price": 5, "live": true, "sku": "ab_c", "at": "2026-01-02T03:04"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "gadget", Title: "Child", ParentID: parent.ID, Fields: map[string]any{"price": 20, "live": false, "sku": "zz"}}); err != nil {
		t.Fatal(err)
	}

	invalid := []string{
		"/v1/content?type=nope",
		"/v1/content?type=gadget&status=draft,bogus",
		"/v1/content?fields.price=1", // no type: nothing is indexed
		"/v1/content?type=gadget&fields.notes=x",
		"/v1/content?type=gadget&fields.price.between=1",
		"/v1/content?type=gadget&fields.price.in=1,abc",
		"/v1/content?type=gadget&fields.price.ne=abc",
		"/v1/content?type=gadget&fields.price.gt=abc",
		"/v1/content?type=gadget&fields.live=maybe",
	}
	for _, path := range invalid {
		code, env := c.do("GET", path, nil)
		if code != http.StatusUnprocessableEntity || env.Error.Code != "validation_failed" {
			t.Errorf("%s: %d %+v", path, code, env)
		}
	}

	count := func(path string) int {
		t.Helper()
		code, env := c.do("GET", path, nil)
		if code != 200 || !env.OK {
			t.Fatalf("%s: %d %+v", path, code, env)
		}
		return env.Meta["total"]
	}
	for path, want := range map[string]int{
		"/v1/content?type=gadget&parent_id=root":             1,
		"/v1/content?type=gadget&parent_id=" + parent.ID:     1,
		"/v1/content?type=gadget&taxonomy.tag=":              2, // empty slug list is ignored
		"/v1/content?type=gadget&fields.price.in=,":          0, // nothing to match
		"/v1/content?type=gadget&fields.sku.like=b_":         1, // _ is literal
		"/v1/content?type=gadget&fields.price.ne=5":          1,
		"/v1/content?type=gadget&fields.price.gte=5":         2,
		"/v1/content?type=gadget&fields.live=1":              1,
		"/v1/content?type=gadget&fields.live=false":          1,
		"/v1/content?type=gadget&fields.at=2026-01-02T03:04": 1,
		"/v1/content?type=gadget&fields.at.gt=not-a-date":    0,
	} {
		if got := count(path); got != want {
			t.Errorf("%s: total %d, want %d", path, got, want)
		}
	}

	// The POST DSL reaches the same validation.
	code, env := c.do("POST", "/v1/content/query", map[string]any{"type": "gadget", "fields": map[string]any{"price": map[string]any{"op": "in", "value": []any{5, 20}}}})
	if code != 200 || env.Meta["total"] != 2 {
		t.Fatalf("query DSL: %d %+v", code, env)
	}
}
