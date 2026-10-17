package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// Comment tools: the list carries no commenter email; approving publishes visitor text, so it
// waits for approval; spam and trash do not.
func TestCommentTools(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	typ, _ := cs.GetType(ctx, "post")
	typ.Supports = append(typ.Supports, "comments")
	cs.SaveType(ctx, *typ)
	it, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Open", Status: "published"})
	svc, _ := kernel.Get[content.CommentService](e.app.Deps, content.ServiceComments)
	c, err := svc.SubmitComment(ctx, content.CommentInput{ContentID: it.ID, AuthorName: "A", AuthorEmail: "secret@b.co", Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	a := e.agent("comments.moderate")
	env, _ := a.call("list_comments", map[string]any{"status": "pending"})
	if data, _ := env["data"].(map[string]any); data == nil || len(data["items"].([]any)) != 1 {
		t.Fatalf("list: %v", env)
	}
	if b, _ := json.Marshal(env); strings.Contains(string(b), "secret@b.co") {
		t.Fatalf("email leaked: %s", b)
	}
	if p := a.ok("moderate_comment", map[string]any{"id": c.ID, "status": "approved"}); str(p, "status") != "pending_approval" {
		t.Fatalf("approve: %v", p)
	}
	if got, _ := svc.GetComment(ctx, c.ID); got.Status != content.CommentPending {
		t.Fatalf("approved before approval: %s", got.Status)
	}
	a.ok("moderate_comment", map[string]any{"id": c.ID, "status": "spam"})
	if got, _ := svc.GetComment(ctx, c.ID); got.Status != content.CommentSpam {
		t.Fatalf("spam: %s", got.Status)
	}
	if names := e.agent("content.view").toolNames(); contains(names, "list_comments") {
		t.Fatal("comment tools need comments.moderate")
	}
}
