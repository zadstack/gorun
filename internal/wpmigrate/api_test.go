package wpmigrate

import (
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// TestAPIMigrationsFlow drives /v1/migrations* like the MCP agent would.
func TestAPIMigrationsFlow(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	e.wp.limitMedia(2, "image/png")
	c := apitest.New(t, e.app, apitest.Options{Scopes: []string{Permission}})

	st, body := c.Post("/v1/migrations/sources", map[string]any{"url": e.wp.URL(), "username": fixtureUser, "app_password": "bad password"})
	if st != 201 {
		t.Fatalf("create source: %d %v", st, body)
	}
	bad := apitest.Data(body)["id"].(string)
	if strings.Contains(c.Do("GET", "/v1/migrations/sources", nil, nil).Body.String(), "bad password") {
		t.Fatal("password echoed by the API")
	}
	if st, body = c.Post("/v1/migrations/sources/"+bad+"/discover", nil); st != 422 || apitest.ErrorCode(body) != "auth_failed" {
		t.Fatalf("discover with bad password: %d %v", st, body)
	}
	_, body = c.Post("/v1/migrations/sources", map[string]any{"url": e.wp.URL(), "username": fixtureUser, "app_password": fixturePass})
	srcID := apitest.Data(body)["id"].(string)
	if st, body = c.Post("/v1/migrations/sources/"+srcID+"/discover", nil); st != 200 || apitest.Data(body)["auth"].(map[string]any)["ok"] != true {
		t.Fatalf("discover: %d %v", st, body)
	}
	if st, body = c.Post("/v1/migrations/sources", map[string]any{"url": "javascript:alert(1)"}); st != 422 {
		t.Fatalf("invalid url: %d %v", st, body)
	}
	st, body = c.Post("/v1/migrations", map[string]any{"source_id": srcID, "options": map[string]any{"slug_policy": "suffix"}})
	if st != 201 {
		t.Fatalf("create migration: %d %v", st, body)
	}
	id := apitest.Data(body)["id"].(string)
	st, body = c.Get("/v1/migrations/" + id + "/mapping")
	if types, _ := apitest.Data(body)["types"].(map[string]any); st != 200 || types == nil || types["product"] == nil {
		t.Fatalf("mapping: %d %v", st, body)
	}
	mapping := apitest.Data(body)
	if st, body = c.Do("PUT", "/v1/migrations/"+id+"/mapping", mapping, nil).Code, nil; st != 200 {
		t.Fatalf("put mapping: %d", st)
	}
	if st, body = c.Post("/v1/migrations/"+id+"/preview", nil); st != 200 || apitest.Data(body)["counts"] == nil {
		t.Fatalf("preview: %d %v", st, body)
	}
	if st, body = c.Post("/v1/migrations/"+id+"/pause", nil); st != 409 {
		t.Fatalf("pause when not running: %d %v", st, body)
	}
	if st, body = c.Post("/v1/migrations/"+id+"/start", nil); st != 200 {
		t.Fatalf("start: %d %v", st, body)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, body = c.Get("/v1/migrations/" + id)
		status := apitest.Data(body)["status"]
		if status == StatusValidated {
			break
		}
		if status == StatusFailed || time.Now().After(deadline) {
			t.Fatalf("status = %v", body)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if st, body = c.Get("/v1/migrations/" + id + "/log?limit=5"); st != 200 || len(body["data"].([]any)) != 5 {
		t.Fatalf("log: %d %v", st, body)
	}
	if st, body = c.Get("/v1/migrations/" + id + "/report"); st != 200 || !strings.Contains(apitest.Data(body)["markdown"].(string), "Migration report") {
		t.Fatalf("report: %d", st)
	}
	if st, _ = c.Get("/v1/migrations/NOPE"); st != 404 {
		t.Fatalf("unknown migration: %d", st)
	}
	if st, _ = c.Get("/v1/migrations"); st != 200 {
		t.Fatalf("list: %d", st)
	}
	// Scope enforcement.
	other := apitest.New(t, e.app, apitest.Options{Scopes: []string{"content.view"}})
	if st, _ = other.Get("/v1/migrations"); st != 403 {
		t.Fatalf("missing scope: %d", st)
	}
}
