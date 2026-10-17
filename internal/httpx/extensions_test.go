package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestExtensionPagesOptInAndRejectOverrides(t *testing.T) {
	for _, dev := range []bool{false, true} {
		tpl := newTestTemplates(t, dev)
		extension := fstest.MapFS{
			"admin/templates/pages/example/index.html": {Data: []byte(`{{define "title"}}Extension{{end}}{{define "content"}}Module-owned page <link href="{{asset "example.css"}}">{{end}}`)},
			"admin/static/example.css":                 {Data: []byte(`.example { color: red; }`)},
		}
		set, err := tpl.set()
		if err != nil {
			t.Fatal(err)
		}
		if set.pages["pages/example/index.html"] != nil {
			t.Fatal("unregistered extension visible")
		}
		if err := tpl.RegisterExtension(extension); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		tpl.Render(rec, httptest.NewRequest("GET", "/admin/example", nil), "pages/example/index.html", nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Module-owned page") || !strings.Contains(rec.Body.String(), "/static/admin/example.css?v=") {
			t.Fatalf("extension render: %d %s", rec.Code, rec.Body.String())
		}
		if err := tpl.RegisterExtension(extension); err == nil {
			t.Fatal("duplicate extension accepted")
		}
		if err := tpl.RegisterExtension(fstest.MapFS{"admin/templates/pages/x.html": {Data: []byte(`{{define "content"}}Hijack{{end}}`)}}); err == nil {
			t.Fatal("core page override accepted")
		}
		if err := tpl.RegisterExtension(fstest.MapFS{"admin/static/admin.css": {Data: []byte(`bad`)}}); err == nil {
			t.Fatal("core asset override accepted")
		}
		set, err = tpl.set()
		if err != nil {
			t.Fatal(err)
		}
		if set.pages["pages/example/index.html"] == nil {
			t.Fatal("valid extension lost after rejected registration")
		}
	}
}
