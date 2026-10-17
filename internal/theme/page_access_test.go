package theme

import (
	"context"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"testing"
)

func TestThemeHelpersExcludeProtectedContent(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Page policy fixture"}`, map[string]string{"templates/index.html": `{{with content (setting "site.test_hidden")}}LEAK {{.Title}}{{else}}HIDDEN{{end}} {{$p:=query (dict "per_page" 1)}}TOTAL={{$p.Total}} {{range $p.Items}}{{.Title}}{{end}}`})
	e := bootRealTheme(t, root)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ctx := context.Background()
	hidden, err := cs.Create(ctx, content.Input{Type: "post", Title: "PRIVATE", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cs.Create(ctx, content.Input{Type: "post", Title: "PUBLIC", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if _, err = cs.(content.PageAccessService).SavePageAccess(adminCtx(), hidden.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	if err = e.app.Deps.Settings.Set(ctx, "site.test_hidden", hidden.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []context.Context{ctx, adminCtx()} {
		rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil, c)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, rec.Body.String(), "HIDDEN", "TOTAL=1 PUBLIC")
		mustNotContain(t, rec.Body.String(), "PRIVATE", "LEAK")
	}
}

func TestProtectedCommentsNeedExactControllerGrant(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", `{"name":"Comment projection fixture"}`, map[string]string{"templates/index.html": `{{range .Items}}{{with comments .}}{{range .Items}}LEAK {{.Body}}{{end}}{{else}}HIDDEN{{end}}{{end}}`})
	e := bootRealTheme(t, root)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	typ, err := cs.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	typ.Supports = append(typ.Supports, "comments")
	if err = cs.SaveType(ctx, *typ); err != nil {
		t.Fatal(err)
	}
	hidden, err := cs.Create(ctx, content.Input{Type: "post", Title: "Protected", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	comments, _ := kernel.Get[content.CommentService](e.app.Deps, content.ServiceComments)
	comment, err := comments.SubmitComment(ctx, content.CommentInput{ContentID: hidden.ID, AuthorName: "Fixture", AuthorEmail: "fixture@example.invalid", Body: "Private comment"})
	if err != nil {
		t.Fatal(err)
	}
	if err = comments.SetCommentStatus(ctx, comment.ID, content.CommentApproved); err != nil {
		t.Fatal(err)
	}
	if _, err = cs.(content.PageAccessService).SavePageAccess(adminCtx(), hidden.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, readctx := range []context.Context{ctx, content.WithDirectPageRead(ctx, "different-id")} {
		rec, err := e.render(kernel.KindRequest{Kind: KindHome}, &Context{Items: []content.Item{*hidden}}, readctx)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, rec.Body.String(), "HIDDEN")
		mustNotContain(t, rec.Body.String(), "Private comment", "LEAK")
	}
}
