package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
)

func TestBuilderPreviewInlinesSavedIdentityWithoutExplicitOverride(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `<!doctype html><html><head>{{builderIdentity}}</head><body>{{builderBody .Content}}</body></html>`})
	e := newEnv(t, root)
	saved := brand.Default()
	saved.Accent = "#167252"
	e.setting("builder.identity", saved)
	item := &content.Item{Type: "page", Slug: "saved-preview", Template: "builder", Locale: "en", Body: "<h1>Draft</h1>"}
	rendered, err := e.svc.RenderBuilderThemePreview(t.Context(), item, BuilderPreviewOptions{Theme: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "data-gorun-builder-identity") || !strings.Contains(rendered, saved.Accent) || strings.Contains(rendered, "/site/builder-identity.css") {
		t.Fatal("saved identity CSS was dropped or left as a network stylesheet in the inert preview")
	}
	// Public rendering keeps its cached stylesheet URL.
	st := &reqState{ctx: t.Context()}
	public := string((&funcs{s: e.svc, st: st}).builderIdentity())
	if !strings.Contains(public, `/site/builder-identity.css?v=`) || strings.Contains(public, "<style") {
		t.Fatal("public identity stylesheet behavior changed")
	}
}
