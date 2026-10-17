package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"strings"
	"testing"
)

func TestThemedBuilderPreviewUsesProposedShellWithoutActivating(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "ACTIVE"})
	writeTheme(t, root, "business", `{"name":"Business","parent":"default"}`, map[string]string{"templates/page-builder.html": `<!doctype html><html data-theme="{{.Theme.Slug}}"><head><link rel="stylesheet" href="{{asset "site.css"}}"><script>alert(1)</script><style>{{builderStyle}}</style></head><body><header>{{.Site.Title}}{{with menu "primary"}}{{range .Items}}<a href="{{.URL}}">{{.Label}}</a>{{end}}{{end}}</header>{{builderBody .Content}}<footer>Designed footer</footer></body></html>`, "assets/site.css": ".designed-shell{display:grid}"})
	e := newEnv(t, root)
	ctx := context.Background()
	item := &content.Item{ID: "preview", Type: "page", Slug: "preview", Title: "Page", Body: "<h1>Draft body</h1>", Locale: "en"}
	html, err := e.svc.RenderBuilderThemePreview(ctx, item, BuilderPreviewOptions{Theme: "business", Title: "Proposed brand", Identity: brand.Default(), Menu: &menus.Menu{Items: []menus.Item{{Label: "Services", URL: "/services"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-theme="business"`, "Proposed brand", "Draft body", "Designed footer", "Services", ".designed-shell", "--gp-accent"} {
		if !strings.Contains(html, want) {
			t.Errorf("preview missing %s", want)
		}
	}
	for _, bad := range []string{"<script", `<base`} {
		if strings.Contains(html, bad) {
			t.Errorf("preview active/blocked resource %s", bad)
		}
	}
	if e.svc.Active(ctx).Slug != "default" {
		t.Fatal("preview activated proposed theme")
	}
}

func TestProposedLogoPreviewDoesNotChangePublicSettings(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.fake.Media.Add(media.Media{ID: "proposed-logo", URL: "/media/proposed-logo.png", Mime: "image/png", Alt: "Proposed studio"})
	e.setting("site.logo", "current-logo")
	item := &content.Item{ID: "preview", Type: "page", Slug: "home", Title: "Studio", Body: "<h1>Studio</h1>", Locale: "en"}
	body, err := e.svc.RenderBuilderThemePreview(t.Context(), item, BuilderPreviewOptions{Theme: "business", Title: "Studio", Logo: "proposed-logo", Identity: brand.Default()})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, "/media/proposed-logo.png")
	if e.app.Deps.Settings.GetString(t.Context(), "site.logo", "") != "current-logo" {
		t.Fatal("preview changed public logo")
	}
}

func TestCustomActiveThemePreviewPreservesOnlyThemeStyles(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/index.html": "ACTIVE"})
	writeTheme(t, root, "custom", `{"name":"Custom","parent":"default"}`, map[string]string{"templates/page-builder.html": `<!doctype html><html><head><link rel="stylesheet" href="/themes/custom/assets/extra.css?v=1"><link rel="stylesheet" href="https://outside.test/evil.css"><link rel="stylesheet" href="/admin/private.css"><script>alert(1)</script><base href="https://outside.test/"></head><body><header>Actual custom header</header>{{builderBody .Content}}</body></html>`, "assets/extra.css": ".custom-title{color:red}"})
	e := newEnv(t, root)
	item := &content.Item{ID: "preview", Type: "page", Slug: "home", Title: "Draft", Body: "<h1>Draft content</h1>", Locale: "en"}
	body, err := e.svc.RenderBuilderThemePreview(t.Context(), item, BuilderPreviewOptions{Theme: "custom", Identity: brand.Default()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Actual custom header", "Draft content", ".custom-title{color:red}"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"outside.test", "/admin/private.css", "<script", "<base"} {
		if strings.Contains(body, bad) {
			t.Errorf("unsafe preview retained %s", bad)
		}
	}
	if e.svc.Active(t.Context()).Slug != "default" {
		t.Fatal("preview changed active theme")
	}
}
