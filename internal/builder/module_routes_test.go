package builder

import (
	"net/http"
	"strings"
	"testing"
)

func TestModuleServesStaticAssetsWithTypesAndCaching(t *testing.T) {
	f := newLayoutFixture(t)
	for path, wantType := range map[string]string{
		"/static/builder/builder.css":         "text/css",
		"/static/builder/hero-scenes.css":     "text/css",
		"/static/builder/hero-scenes.js":      "text/javascript",
		"/static/builder/three.module.min.js": "text/javascript",
		"/static/builder/three.core.min.js":   "text/javascript",
	} {
		resp, err := http.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), wantType) || resp.Header.Get("Cache-Control") != "public, max-age=3600" {
			t.Fatalf("%s: HTTP %d type %q cache %q", path, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
		}
	}
}

func TestModuleAdminRoutesRequireSessionAndRenderForAdmins(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	page := f.page(t, admin, "Routed page")
	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if status, _ := f.request(t, anonymous, "GET", "/admin/appearance/readiness", "", "", nil); status == 200 {
		t.Fatal("anonymous readiness access")
	}
	client, csrf := f.session(t, admin, page.ID)
	status, raw := f.request(t, client, "GET", "/admin/appearance/readiness", "", "", nil)
	if status != 200 || !strings.Contains(string(raw), "<h2") {
		t.Fatalf("readiness HTTP %d", status)
	}
	if status, raw = f.request(t, client, "GET", "/admin/builder/"+page.ID, "", "", nil); status != 200 || !strings.Contains(string(raw), `data-id="`+page.ID+`"`) {
		t.Fatalf("editor route HTTP %d", status)
	}
	if status, _ = f.request(t, client, "GET", "/admin/site-builder", "", "", nil); status != 200 {
		t.Fatalf("site builder route HTTP %d", status)
	}
	status, _ = f.request(t, client, "POST", "/admin/builder/"+page.ID+"/save", "", csrf, `{"document":{"version":1,"sections":[]},"base_updated_at":"stale"}`)
	if status != 409 {
		t.Fatalf("routed stale save HTTP %d", status)
	}
}
