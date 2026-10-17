package site_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// openComments enables comments on posts and publishes one.
func openComments(t *testing.T, e *env) (content.CommentService, *content.Item) {
	t.Helper()
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	typ, err := cs.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	typ.Supports = append(typ.Supports, "comments")
	if err := cs.SaveType(ctx, *typ); err != nil {
		t.Fatal(err)
	}
	comments, ok := kernel.Get[content.CommentService](e.app.Deps, content.ServiceComments)
	if !ok {
		t.Fatal("no comment service")
	}
	return comments, publish(t, cs, content.Input{Type: "post", Slug: "hello", Title: "Hello", Locale: "ar"})
}

func TestCommentsOnTheSite(t *testing.T) {
	e := bootReal(t)
	comments, it := openComments(t, e)
	_, page := e.get("/post/hello")
	mustContain(t, page, `id="comments"`, `action="http://example.test/forms/comment"`, `name="content_id" value="`+it.ID+`"`,
		`name="_back" value="/post/hello"`, `name="website"`)
	token := csrfFrom(t, page)
	form := url.Values{"_csrf": {token}, "content_id": {it.ID}, "_back": {"/post/hello"}, "name": {"Sara"},
		"email": {"sara-secret@example.com"}, "body": {`<script>alert(1)</script> hi`}}

	resp, _ := e.do("POST", "/forms/comment", form)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/post/hello#comments" {
		t.Fatalf("submit: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page = e.get("/post/hello")
	mustContain(t, page, `data-comment-flash="pending"`)
	mustNotContain(t, page, "alert(1)") // pending: not shown

	list, _ := comments.Comments(context.Background(), content.CommentQuery{Status: content.CommentPending})
	if list.Total != 1 {
		t.Fatalf("stored: %+v", list)
	}
	if err := comments.SetCommentStatus(context.Background(), list.Items[0].ID, content.CommentApproved); err != nil {
		t.Fatal(err)
	}
	_, page = e.get("/post/hello")
	mustContain(t, page, "Sara", "&lt;script&gt;alert(1)&lt;/script&gt; hi")
	mustNotContain(t, page, "<script>alert(1)", "sara-secret@example.com")

	// honeypot: answered like a success, nothing stored
	bot := url.Values{"_csrf": {token}, "content_id": {it.ID}, "_back": {"/post/hello"}, "name": {"Bot"},
		"email": {"bot@example.com"}, "body": {"spam"}, "website": {"http://spam.test"}}
	if resp, _ = e.do("POST", "/forms/comment", bot); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("honeypot: %d", resp.StatusCode)
	}
	// invalid: back to the item with an error flash
	bad := url.Values{"_csrf": {token}, "content_id": {it.ID}, "_back": {"/post/hello"}, "name": {""}, "email": {"x"}, "body": {"x"}}
	if resp, _ = e.do("POST", "/forms/comment", bad); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("invalid: %d", resp.StatusCode)
	}
	_, page = e.get("/post/hello")
	mustContain(t, page, `data-comment-flash="invalid"`)
	// no CSRF: refused
	noCSRF := url.Values{"content_id": {it.ID}, "name": {"A"}, "email": {"a@b.co"}, "body": {"x"}}
	if resp, _ = e.do("POST", "/forms/comment", noCSRF); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf: %d", resp.StatusCode)
	}
	if all, _ := comments.Comments(context.Background(), content.CommentQuery{}); all.Total != 1 {
		t.Fatalf("only the first comment is stored: %d", all.Total)
	}
	// a type without comments shows no form
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	publish(t, cs, content.Input{Type: "page", Slug: "about", Title: "About"})
	if _, page = e.get("/about"); strings.Contains(page, `id="comments"`) {
		t.Fatal("comments on a type without them")
	}
}

// The English translation posts its comments back to its /en/ URL.
func TestCommentFormUnderLocalePrefix(t *testing.T) {
	e := bootReal(t)
	_, it := openComments(t, e)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	translate(t, cs, it, "Hello EN", true)
	_, page := e.get("/en/post/hello")
	mustContain(t, page, `name="_back" value="/en/post/hello"`)
}
