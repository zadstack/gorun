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

func TestAPITermTranslations(t *testing.T) {
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
	user, _ = servicesAuth(t, app)
	call := func(method, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
		var env map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		return rec.Code, env
	}
	data := func(env map[string]any) map[string]any { return env["data"].(map[string]any) }

	code, env := call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": "News", "locale": "en"})
	if code != 201 || data(env)["locale"] != "en" || data(env)["translation_group"] != "" {
		t.Fatalf("create with locale: %d %v", code, env)
	}
	news := data(env)["id"].(string)
	// translation_of creates the term as a translation of another one.
	code, env = call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": "أخبار", "locale": "ar", "translation_of": news})
	if code != 201 || data(env)["locale"] != "ar" || data(env)["translation_group"] == "" {
		t.Fatalf("create as translation: %d %v", code, env)
	}
	akhbar := data(env)["id"].(string)
	group := data(env)["translation_group"].(string)
	if code, env = call("POST", "/v1/taxonomies/tag/terms", map[string]any{"name": "x", "locale": "ar", "translation_of": news}); code != 422 {
		t.Fatalf("translation_of across taxonomies: %d %v", code, env)
	}
	if code, env = call("POST", "/v1/taxonomies/category/terms", map[string]any{"name": "y", "locale": "fr"}); code != 422 {
		t.Fatalf("unknown locale: %d %v", code, env)
	}
	// Filters.
	code, env = call("GET", "/v1/taxonomies/category/terms?locale=ar", nil)
	if code != 200 || env["meta"].(map[string]any)["total"] != 1.0 {
		t.Fatalf("locale filter: %d %v", code, env)
	}
	code, env = call("GET", "/v1/taxonomies/category/terms?translation_group="+group, nil)
	if code != 200 || env["meta"].(map[string]any)["total"] != 2.0 {
		t.Fatalf("group filter: %d %v", code, env)
	}
	// The translations sub-resource.
	code, env = call("GET", "/v1/taxonomies/category/terms/"+news+"/translations", nil)
	if code != 200 || len(env["data"].([]any)) != 2 {
		t.Fatalf("list translations: %d %v", code, env)
	}
	if code, env = call("DELETE", "/v1/taxonomies/category/terms/"+akhbar+"/translations", nil); code != 200 || len(env["data"].([]any)) != 1 {
		t.Fatalf("unlink: %d %v", code, env)
	}
	if code, env = call("POST", "/v1/taxonomies/category/terms/"+news+"/translations/link", map[string]any{"term_id": akhbar}); code != 200 || len(env["data"].([]any)) != 2 {
		t.Fatalf("link: %d %v", code, env)
	}
	code, env = call("POST", "/v1/taxonomies/category/terms/"+news+"/translations", map[string]any{"locale": "ar", "name": "ثانية"})
	if code != 422 {
		t.Fatalf("second translation in a taken locale: %d %v", code, env)
	}
	if code, _ = call("GET", "/v1/taxonomies/tag/terms/"+news+"/translations", nil); code != 404 {
		t.Fatalf("translations of a term in the wrong taxonomy: %d", code)
	}
	// PATCH never moves a term between groups.
	code, env = call("PATCH", "/v1/taxonomies/category/terms/"+news, map[string]any{"translation_group": "", "description": "d"})
	if code != 200 || data(env)["translation_group"] != group {
		t.Fatalf("patch kept the group: %d %v", code, env)
	}
	user, _ = servicesAuthRole(t, app, "contributor")
	if code, _ = call("POST", "/v1/taxonomies/category/terms/"+news+"/translations", map[string]any{"locale": "ar", "name": "z"}); code != 403 {
		t.Fatalf("contributor translated: %d", code)
	}
	if code, _ = call("GET", "/v1/taxonomies/category/terms/"+news+"/translations", nil); code != 200 {
		t.Fatalf("contributor reads translations: %d", code)
	}
}
