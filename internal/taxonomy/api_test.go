package taxonomy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
)

func TestAPITaxonomiesAndTerms(t *testing.T) {
	app := bootApp(t)
	r := chi.NewRouter()
	var user *auth.User
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), user)))
		})
	})
	for _, op := range app.Deps.API.Operations() {
		r.Method(op.Method, op.Path, op.Handler)
	}
	us, _ := servicesAuth(t, app)
	user = us
	call := func(method, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
		var env map[string]any
		json.Unmarshal(rec.Body.Bytes(), &env)
		return rec.Code, env
	}
	code, env := call("GET", "/v1/taxonomies", nil)
	if code != 200 || env["ok"] != true || len(env["data"].([]any)) != 2 {
		t.Fatalf("list: %d %v", code, env)
	}
	code, env = call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": "أخبار"})
	if code != 201 {
		t.Fatalf("create term: %d %v", code, env)
	}
	term := env["data"].(map[string]any)
	if term["slug"] != "أخبار" {
		t.Fatalf("term: %v", term)
	}
	id := term["id"].(string)
	code, env = call("PATCH", "/v1/taxonomies/category/terms/"+id, map[string]any{"description": "d"})
	if code != 200 || env["data"].(map[string]any)["slug"] != "أخبار" || env["data"].(map[string]any)["description"] != "d" {
		t.Fatalf("patch keeps slug: %d %v", code, env)
	}
	code, env = call("GET", "/v1/taxonomies/category/terms?search=أخ", nil)
	if code != 200 || env["meta"].(map[string]any)["total"] != 1.0 {
		t.Fatalf("terms list: %d %v", code, env)
	}
	if code, _ = call("GET", "/v1/taxonomies/tag/terms/"+id, nil); code != 404 {
		t.Fatalf("term in the wrong taxonomy: %d", code)
	}
	if code, env = call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": ""}); code != 422 || env["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("validation: %d %v", code, env)
	}
	if code, _ = call("DELETE", "/v1/taxonomies/tag", nil); code != 409 {
		t.Fatalf("system delete: %d", code)
	}
	if code, _ = call("DELETE", "/v1/taxonomies/category/terms/"+id, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	user, _ = servicesAuthRole(t, app, "contributor")
	if code, _ = call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": "x"}); code != 403 {
		t.Fatalf("contributor: %d", code)
	}
}
