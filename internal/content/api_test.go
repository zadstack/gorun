package content_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Meta  map[string]int  `json:"meta"`
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

// apiClient serves the registered API operations as user (the api module
// does the routing in production).
type apiClient struct {
	t    *testing.T
	r    chi.Router
	user *auth.User
}

func newAPIClient(t *testing.T, app *kernel.App, u *auth.User) *apiClient {
	r := chi.NewRouter()
	c := &apiClient{t: t, r: r, user: u}
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), c.user)))
		})
	})
	for _, op := range app.Deps.API.Operations() {
		r.Method(op.Method, op.Path, op.Handler)
	}
	return c
}

func (c *apiClient) do(method, path string, body any) (int, envelope) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		c.t.Fatalf("%s %s: not an envelope: %s", method, path, rec.Body.String())
	}
	return rec.Code, env
}

func TestAPIContentCRUDEnvelope(t *testing.T) {
	app := bootApp(t)
	admin := mustUser(t, app, "admin@example.com", "admin")
	c := newAPIClient(t, app, admin)

	code, env := c.do("POST", "/v1/types", map[string]any{"slug": "product", "name": map[string]string{"en": "Products"}, "public": true,
		"fields": []map[string]any{{"key": "price", "type": "number", "indexed": true, "required": true}}})
	if code != 201 || !env.OK {
		t.Fatalf("create type: %d %+v", code, env)
	}
	code, env = c.do("POST", "/v1/content", map[string]any{"type": "product", "title": "Plan", "fields": map[string]any{}})
	if code != 422 || env.OK || env.Error.Code != "validation_failed" || env.Error.Fields["price"] == "" || env.Error.Fields["price"] == "content.validation.required" {
		t.Fatalf("validation envelope (translated): %d %+v", code, env)
	}
	code, env = c.do("POST", "/v1/content", map[string]any{"type": "product", "title": "Plan", "fields": map[string]any{"price": 9, "extra": "x"}})
	if code != 201 || !env.OK {
		t.Fatalf("create: %d %+v", code, env)
	}
	var it map[string]any
	json.Unmarshal(env.Data, &it)
	id := it["id"].(string)
	if it["author_id"] != admin.ID || it["status"] != "draft" || it["fields"].(map[string]any)["extra"] != nil {
		t.Fatalf("created item: %v", it)
	}

	code, env = c.do("PATCH", "/v1/content/"+id, map[string]any{"title": "Plan Pro"})
	json.Unmarshal(env.Data, &it)
	if code != 200 || it["title"] != "Plan Pro" || it["fields"].(map[string]any)["price"] != 9.0 {
		t.Fatalf("patch keeps omitted values: %d %v", code, it)
	}
	code, env = c.do("GET", "/v1/content?type=product&fields.price.gte=5&order=price:desc", nil)
	var items []map[string]any
	json.Unmarshal(env.Data, &items)
	if code != 200 || len(items) != 1 || env.Meta["total"] != 1 || env.Meta["page"] != 1 || env.Meta["per_page"] != 20 {
		t.Fatalf("list: %d %s %v", code, env.Data, env.Meta)
	}
	if code, env = c.do("GET", "/v1/content?type=product&fields.title.eq=x", nil); code != 422 {
		t.Fatalf("filter on non-indexed key: %d", code)
	}
	code, env = c.do("POST", "/v1/content/"+id+"/publish", nil)
	json.Unmarshal(env.Data, &it)
	if code != 200 || it["status"] != "published" || it["published_at"] == "" {
		t.Fatalf("publish: %d %v", code, it)
	}
	code, env = c.do("GET", "/v1/content/"+id+"/revisions", nil)
	var revs []map[string]any
	json.Unmarshal(env.Data, &revs)
	if code != 200 || len(revs) != 3 {
		t.Fatalf("revisions: %d %d", code, len(revs))
	}
	if code, _ = c.do("GET", "/v1/content/nope", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}

	// An author may not touch someone else's item, nor manage types.
	c.user = mustUser(t, app, "author@example.com", "author")
	if code, env = c.do("PATCH", "/v1/content/"+id, map[string]any{"title": "Hijack"}); code != 403 || env.Error.Code != "forbidden" {
		t.Fatalf("author edit other: %d %+v", code, env)
	}
	if code, _ = c.do("POST", "/v1/types", map[string]any{"slug": "x"}); code != 403 {
		t.Fatalf("author types.manage: %d", code)
	}
	code, env = c.do("POST", "/v1/content", map[string]any{"type": "post", "title": "Mine", "author_id": admin.ID})
	json.Unmarshal(env.Data, &it)
	if code != 201 || it["author_id"] != c.user.ID {
		t.Fatalf("author creates own: %d %v", code, it)
	}

	c.user = admin
	if code, _ = c.do("DELETE", "/v1/content/"+id, nil); code != 200 {
		t.Fatalf("trash: %d", code)
	}
	_, env = c.do("GET", "/v1/content/"+id, nil)
	json.Unmarshal(env.Data, &it)
	if it["status"] != "trash" {
		t.Fatalf("after trash: %v", it)
	}
	if code, _ = c.do("DELETE", "/v1/content/"+id+"?hard=1", nil); code != 200 {
		t.Fatalf("hard delete: %d", code)
	}
	if code, _ = c.do("GET", "/v1/content/"+id, nil); code != 404 {
		t.Fatalf("after hard delete: %d", code)
	}
	if code, env = c.do("DELETE", "/v1/types/post", nil); code != 409 {
		t.Fatalf("system type delete: %d %+v", code, env)
	}
}

func TestAPITypesAndRevisionRestore(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "admin@example.com", "admin"))
	if code, env := c.do("GET", "/v1/types", nil); code != 200 || !env.OK {
		t.Fatalf("list types: %d", code)
	}
	code, env := c.do("PATCH", "/v1/types/post", map[string]any{"icon": "newspaper", "slug": "ignored"})
	var typ map[string]any
	json.Unmarshal(env.Data, &typ)
	if code != 200 || typ["slug"] != "post" || typ["icon"] != "newspaper" || typ["system"] != true {
		t.Fatalf("patch type: %d %v", code, typ)
	}
	if code, _ = c.do("GET", "/v1/types/nope", nil); code != 404 {
		t.Fatalf("missing type: %d", code)
	}
	_, env = c.do("POST", "/v1/content", map[string]any{"type": "post", "title": "v1"})
	var it map[string]any
	json.Unmarshal(env.Data, &it)
	id := it["id"].(string)
	c.do("PATCH", "/v1/content/"+id, map[string]any{"title": "v2"})
	_, env = c.do("GET", "/v1/content/"+id+"/revisions", nil)
	var revs []map[string]any
	json.Unmarshal(env.Data, &revs)
	oldest := revs[len(revs)-1]["id"].(string)
	code, env = c.do("POST", "/v1/content/"+id+"/revisions/"+oldest+"/restore", nil)
	json.Unmarshal(env.Data, &it)
	if code != 200 || it["title"] != "v1" {
		t.Fatalf("restore: %d %v", code, it)
	}
	code, env = c.do("POST", "/v1/content/query", map[string]any{"type": "post", "search": "v1"})
	if code != 200 || env.Meta["total"] != 1 {
		t.Fatalf("query: %d %v", code, env.Meta)
	}
	if code, _ = c.do("POST", "/v1/content/"+id+"/unpublish", nil); code != 200 {
		t.Fatalf("unpublish: %d", code)
	}
	if code, _ = c.do("POST", "/v1/types", map[string]any{"slug": "Bad Slug"}); code != 422 {
		t.Fatalf("invalid type: %d", code)
	}
	if code, _ = c.do("POST", "/v1/types", map[string]any{"slug": "tmp", "name": map[string]string{"en": "Tmp"}}); code != 201 {
		t.Fatalf("create tmp: %d", code)
	}
	if code, _ = c.do("DELETE", "/v1/types/tmp", nil); code != 200 {
		t.Fatalf("delete tmp: %d", code)
	}
}

func TestAPITranslations(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "admin@example.com", "admin"))
	idOf := func(env envelope) string {
		var it map[string]any
		json.Unmarshal(env.Data, &it)
		id, _ := it["id"].(string)
		return id
	}
	_, env := c.do("POST", "/v1/content", map[string]any{"type": "post", "title": "عربي", "locale": "ar"})
	ar := idOf(env)
	code, env := c.do("POST", "/v1/content/"+ar+"/translations", map[string]any{"locale": "en"})
	en := idOf(env)
	if code != 201 || en == "" {
		t.Fatalf("create translation: %d %s", code, env.Data)
	}
	if code, env = c.do("POST", "/v1/content/"+ar+"/translations", map[string]any{"locale": "en"}); code != 422 {
		t.Fatalf("taken locale: %d %s", code, env.Data)
	}
	code, env = c.do("GET", "/v1/content/"+ar+"/translations", nil)
	var list []map[string]any
	json.Unmarshal(env.Data, &list)
	if code != 200 || len(list) != 2 || list[0]["translation_group"] == "" {
		t.Fatalf("list: %d %s", code, env.Data)
	}
	if code, _ = c.do("DELETE", "/v1/content/"+en+"/translations", nil); code != 200 {
		t.Fatalf("unlink: %d", code)
	}
	if code, env = c.do("POST", "/v1/content/"+ar+"/translations/link", map[string]any{"id": en}); code != 200 {
		t.Fatalf("link: %d %s", code, env.Data)
	}
	json.Unmarshal(env.Data, &list)
	if len(list) != 2 {
		t.Fatalf("link returns the group: %s", env.Data)
	}
	if code, _ = c.do("GET", "/v1/content/nope/translations", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}
}

func TestAPIComments(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "admin@example.com", "admin"))
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	typ, _ := cs.GetType(ctx, "post")
	typ.Supports = append(typ.Supports, "comments")
	cs.SaveType(ctx, *typ)
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Open", Status: "published"})
	comments, _ := kernel.Get[content.CommentService](app.Deps, content.ServiceComments)
	cm, err := comments.SubmitComment(ctx, content.CommentInput{ContentID: it.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	code, env := c.do("GET", "/v1/comments?status=pending", nil)
	var list []map[string]any
	json.Unmarshal(env.Data, &list)
	if code != 200 || len(list) != 1 || list[0]["id"] != cm.ID {
		t.Fatalf("list: %d %s", code, env.Data)
	}
	if code, env = c.do("PATCH", "/v1/comments/"+cm.ID, map[string]any{"status": "approved"}); code != 200 {
		t.Fatalf("patch: %d %s", code, env.Data)
	}
	if code, _ = c.do("PATCH", "/v1/comments/"+cm.ID, map[string]any{"status": "nope"}); code != 422 {
		t.Fatalf("bad status: %d", code)
	}
	if code, _ = c.do("DELETE", "/v1/comments/"+cm.ID, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = c.do("DELETE", "/v1/comments/"+cm.ID, nil); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}
