package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"testing"
)

func TestThemeHelpersExcludeNonPublicTypes(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Public helper fixture"}`, map[string]string{"templates/index.html": `{{with content (setting "site.test_hidden")}}LEAK {{.Title}}{{else}}HIDDEN{{end}} {{$p := query (dict "per_page" 1)}}TOTAL={{$p.Total}} {{range $p.Items}}{{.Title}}{{end}} {{$n := query (dict "type" "invoice")}}PRIVATE={{$n.Total}}`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if err := cs.SaveType(ctx, content.Type{Slug: "invoice", Public: false, Name: map[string]string{"en": "Invoice"}}); err != nil {
		t.Fatal(err)
	}
	hidden, err := cs.Create(ctx, content.Input{Type: "invoice", Title: "INTERNAL", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "PUBLIC", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Deps.Settings.Set(ctx, "site.test_hidden", hidden.ID); err != nil {
		t.Fatal(err)
	}
	for _, readctx := range []context.Context{ctx, adminCtx()} {
		backend, err := cs.Query(readctx, content.Query{Type: "invoice"})
		if err != nil || backend.Total != 1 {
			t.Fatalf("backend internal type query: total=%d error=%v", backend.Total, err)
		}
		rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil, readctx)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, rec.Body.String(), "HIDDEN", "TOTAL=1 PUBLIC", "PRIVATE=0")
		mustNotContain(t, rec.Body.String(), "LEAK", "INTERNAL")
	}
}
