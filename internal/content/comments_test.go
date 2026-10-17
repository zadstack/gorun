package content

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// commentsOpen returns a service whose post type supports comments, and a published post.
func commentsOpen(t *testing.T) (*service, *Item) {
	t.Helper()
	s := newTestService(t)
	ctx := context.Background()
	post, err := s.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	post.Supports = append(post.Supports, "comments")
	if err := s.SaveType(ctx, *post); err != nil {
		t.Fatal(err)
	}
	return s, mustCreate(t, s, Input{Type: "post", Title: "Open", Status: "published"})
}

func TestSubmitCommentRules(t *testing.T) {
	s, it := commentsOpen(t)
	ctx := context.Background()
	ok := CommentInput{ContentID: it.ID, AuthorName: "Sara", AuthorEmail: "sara@example.com", Body: "Nice <b>post</b>"}
	c, err := s.SubmitComment(ctx, ok)
	if err != nil || c.Status != CommentPending || c.Body != "Nice <b>post</b>" {
		t.Fatalf("submit: %+v %v", c, err)
	}
	draft := mustCreate(t, s, Input{Type: "post", Title: "Draft"})
	page := mustCreate(t, s, Input{Type: "page", Title: "Page", Status: "published"})
	for name, in := range map[string]CommentInput{
		"draft":          {ContentID: draft.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "x"},
		"no-type":        {ContentID: page.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "x"},
		"missing":        {ContentID: "nope", AuthorName: "A", AuthorEmail: "a@b.co", Body: "x"},
		"no-name":        {ContentID: it.ID, AuthorEmail: "a@b.co", Body: "x"},
		"bad-email":      {ContentID: it.ID, AuthorName: "A", AuthorEmail: "nope", Body: "x"},
		"no-body":        {ContentID: it.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "  "},
		"long-body":      {ContentID: it.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: strings.Repeat("x", maxCommentBody+1)},
		"pending-parent": {ContentID: it.ID, ParentID: c.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "x"},
	} {
		var ve *ValidationError
		if _, err := s.SubmitComment(ctx, in); !errors.As(err, &ve) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
	// one reply level, under an approved comment of the same item
	if err := s.SetCommentStatus(ctx, c.ID, CommentApproved); err != nil {
		t.Fatal(err)
	}
	reply, err := s.SubmitComment(ctx, CommentInput{ContentID: it.ID, ParentID: c.ID, AuthorName: "B", AuthorEmail: "b@b.co", Body: "Thanks"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetCommentStatus(ctx, reply.ID, CommentApproved)
	var ve *ValidationError
	if _, err := s.SubmitComment(ctx, CommentInput{ContentID: it.ID, ParentID: reply.ID, AuthorName: "C", AuthorEmail: "c@b.co", Body: "deep"}); !errors.As(err, &ve) {
		t.Fatalf("a reply to a reply: %v", err)
	}
	other := mustCreate(t, s, Input{Type: "post", Title: "Other", Status: "published"})
	if _, err := s.SubmitComment(ctx, CommentInput{ContentID: other.ID, ParentID: c.ID, AuthorName: "C", AuthorEmail: "c@b.co", Body: "x"}); !errors.As(err, &ve) {
		t.Fatalf("a parent on another item: %v", err)
	}
}

func TestCommentModerationAndListing(t *testing.T) {
	s, it := commentsOpen(t)
	ctx := context.Background()
	a, _ := s.SubmitComment(ctx, CommentInput{ContentID: it.ID, AuthorName: "A", AuthorEmail: "a@b.co", Body: "first"})
	b, _ := s.SubmitComment(ctx, CommentInput{ContentID: it.ID, AuthorName: "B", AuthorEmail: "b@b.co", Body: "second"})
	if list, _ := s.ApprovedComments(ctx, it.ID, 50); len(list) != 0 {
		t.Fatalf("pending shown: %v", list)
	}
	if err := s.SetCommentStatus(ctx, a.ID, CommentApproved); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentStatus(ctx, b.ID, CommentSpam); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentStatus(ctx, b.ID, "bogus"); err == nil {
		t.Fatal("unknown status accepted")
	}
	list, _ := s.ApprovedComments(ctx, it.ID, 50)
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("approved: %v", list)
	}
	page, err := s.Comments(ctx, CommentQuery{Status: CommentSpam})
	if err != nil || page.Total != 1 || page.Items[0].ID != b.ID || page.Items[0].ContentTitle != "Open" {
		t.Fatalf("spam list: %+v %v", page, err)
	}
	counts, _ := s.CommentCounts(ctx)
	if counts[CommentApproved] != 1 || counts[CommentSpam] != 1 || counts[CommentPending] != 0 {
		t.Fatalf("counts: %v", counts)
	}
	if err := s.DeleteComment(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetComment(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	// deleting the item for good takes its comments
	if err := s.Delete(ctx, it.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetComment(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascade: %v", err)
	}
}
