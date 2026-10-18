package theme

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func decode(t *testing.T, body string) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("bad envelope %q: %v", body, err)
	}
	return e
}

func TestAPIThemesAndTemplates(t *testing.T) {
	e := newEnv(t, adminThemes(t))
	// The api module (task 9) routes registered operations; mount them the
	// same way here, behind the real API auth middleware.
	ops := e.app.Deps.API.Operations()
	want := map[string]bool{"GET /v1/themes": true, "POST /v1/themes/{slug}/activate": true, "GET /v1/templates": true,
		"GET /v1/templates/{name}": true, "PUT /v1/templates/{name}/draft": true, "POST /v1/templates/{name}/publish": true, "DELETE /v1/templates/{name}/override": true}
	for _, op := range ops {
		delete(want, op.Method+" "+op.Path)
		if len(op.Scopes) == 0 || op.Scopes[0] != PermManage {
			t.Fatalf("%s %s scopes: %v", op.Method, op.Path, op.Scopes)
		}
		e.app.Deps.Router.API.Method(op.Method, op.Path, op.Handler)
	}
	if len(want) != 0 {
		t.Fatalf("missing operations: %v", want)
	}
	c := e.client()
	if resp, _ := c.get("/api/v1/themes"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	editor := e.client()
	e.login(editor, "ed@example.com", "editor")
	if resp, body := editor.get("/api/v1/themes"); resp.StatusCode != http.StatusForbidden || decode(t, body).Error.Code != "forbidden" {
		t.Fatalf("editor: %d %s", resp.StatusCode, body)
	}

	token := e.login(c, "admin@example.com", "admin")
	jsonHdr := []string{"Content-Type", "application/json", "X-CSRF-Token", token}

	resp, body := c.get("/api/v1/themes")
	env := decode(t, body)
	if resp.StatusCode != 200 || !env.OK || !strings.Contains(string(env.Data), `"slug":"child"`) || !strings.Contains(string(env.Data), `"active":true`) {
		t.Fatalf("list themes: %d %s", resp.StatusCode, body)
	}
	if resp, body := c.do("POST", "/api/v1/themes/nope/activate", nil, jsonHdr...); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("activate unknown: %d %s", resp.StatusCode, body)
	}
	if resp, body := c.do("POST", "/api/v1/themes/child/activate", nil, jsonHdr...); resp.StatusCode != 200 {
		t.Fatalf("activate: %d %s", resp.StatusCode, body)
	}
	if got := e.svc.Active(t.Context()).Slug; got != "child" {
		t.Fatalf("active = %q", got)
	}

	resp, body = c.get("/api/v1/templates")
	if resp.StatusCode != 200 || !strings.Contains(body, `"name":"page.html"`) || !strings.Contains(body, `"source":"parent"`) {
		t.Fatalf("templates: %d %s", resp.StatusCode, body)
	}
	resp, body = c.get("/api/v1/templates/" + url.PathEscape("parts.header.html"))
	if resp.StatusCode != 200 || !strings.Contains(body, "BASE-HEADER") {
		t.Fatalf("read template: %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.get("/api/v1/templates/nope.html"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing template: %d", resp.StatusCode)
	}

	// Draft via PUT (MCP-style), invisible until published.
	resp, body = c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":"API-DRAFT {{.Kind}}"}`), jsonHdr...)
	if resp.StatusCode != 200 {
		t.Fatalf("put draft: %d %s", resp.StatusCode, body)
	}
	for _, malformed := range []string{`{}`, `null`, `{"body":null}`, `{"body":"replacement"} {}`, `{"body":"replacement"} trailing`} {
		resp, response := c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(malformed), jsonHdr...)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed draft %q: %d %s", malformed, resp.StatusCode, response)
		}
		if draft, ok := e.svc.readDraft(t.Context(), "single.html"); !ok || draft != "API-DRAFT {{.Kind}}" {
			t.Fatalf("malformed request replaced existing draft: %q", draft)
		}
	}
	_, body = c.get("/api/v1/templates/single.html")
	if !strings.Contains(body, `"draft":"API-DRAFT {{.Kind}}"`) || !strings.Contains(body, "BASE-SINGLE") {
		t.Fatalf("read with draft: %s", body)
	}
	resp, body = c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":"{{ nosuchfunc }}"}`), jsonHdr...)
	if resp.StatusCode != http.StatusUnprocessableEntity || decode(t, body).Error.Code != "validation_failed" {
		t.Fatalf("invalid draft: %d %s", resp.StatusCode, body)
	}
	if resp, _ := c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`not json`), jsonHdr...); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json: %d", resp.StatusCode)
	}
	if resp, _ := c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":"x"}`), "Content-Type", "application/json"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("draft without csrf header: %d", resp.StatusCode)
	}
	if resp, body := c.do("POST", "/api/v1/templates/single.html/publish", nil, jsonHdr...); resp.StatusCode != 200 {
		t.Fatalf("publish: %d %s", resp.StatusCode, body)
	}
	if got, _ := e.svc.ReadTemplate(t.Context(), "single.html"); got != "API-DRAFT {{.Kind}}" {
		t.Fatalf("published = %q", got)
	}
	if resp, _ := c.do("POST", "/api/v1/templates/single.html/publish", nil, jsonHdr...); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("publish without draft: %d", resp.StatusCode)
	}
	if resp, response := c.do("PUT", "/api/v1/templates/single.html/draft", strings.NewReader(`{"body":""}`), jsonHdr...); resp.StatusCode != http.StatusOK {
		t.Fatalf("explicit empty source: %d %s", resp.StatusCode, response)
	}
	if draft, ok := e.svc.readDraft(t.Context(), "single.html"); !ok || draft != "" {
		t.Fatalf("explicit empty draft not saved: %q", draft)
	}
	if resp, _ := c.do("PUT", "/api/v1/templates/bad..name/draft", strings.NewReader(`{"body":"x"}`), jsonHdr...); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad name: %d", resp.StatusCode)
	}
}
