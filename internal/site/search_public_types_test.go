package site_test

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchPaginatesOnlyPublicTypes(t *testing.T) {
	root := t.TempDir()
	templates := filepath.Join(root, "default", "templates")
	if err := os.MkdirAll(templates, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "search.html"), []byte(`TOTAL={{.Pagination.Total}} {{range .Items}}<{{.Title}}>{{end}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "default", "theme.json"), []byte(`{"name":"Search fixture"}`), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), SecretKey: make([]byte, 32), Theme: "default", BaseURL: "http://example.test"}
	app, err := kernel.Boot(ctx, cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), theme.New(theme.WithThemesDir(root)), site.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	e := &env{t: t, app: app, srv: srv, c: &http.Client{}}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if err := cs.SaveType(ctx, content.Type{Slug: "invoice", Public: false, Name: map[string]string{"en": "Invoice"}}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []content.Input{{Type: "invoice", Title: "Needle private", Body: "needle needle needle"}, {Type: "post", Title: "Needle public one"}, {Type: "post", Title: "Needle public two"}} {
		publish(t, cs, in)
	}
	e.setting("site.posts_per_page", 1)
	for _, page := range []string{"1", "2"} {
		resp, body := e.get("/search?q=needle&page=" + page)
		if resp.StatusCode != 200 {
			t.Fatalf("search: %d %s", resp.StatusCode, body)
		}
		mustContain(t, body, "TOTAL=2", "Needle public")
		mustNotContain(t, body, "Needle private")
	}
}
