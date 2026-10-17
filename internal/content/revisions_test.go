package content

import (
	"context"
	"fmt"
	"testing"
)

func TestRevisionCreatedOnUpdateAndCapped25(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "post", Title: "v0"})
	revs, _ := s.Revisions(ctx, it.ID)
	if len(revs) != 1 {
		t.Fatalf("create should snapshot: %d", len(revs))
	}
	for i := 1; i <= 30; i++ {
		in := InputFromItem(it)
		in.Title = fmt.Sprintf("v%d", i)
		var err error
		if it, err = s.Update(ctx, it.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	revs, err := s.Revisions(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 25 || revs[0].Snapshot.Title != "v30" || revs[24].Snapshot.Title != "v6" {
		t.Fatalf("revisions: %d first %q last %q", len(revs), revs[0].Snapshot.Title, revs[len(revs)-1].Snapshot.Title)
	}
	s.settings.Set(ctx, "content.revisions_keep", 3)
	in := InputFromItem(it)
	in.Title = "v31"
	s.Update(ctx, it.ID, in)
	if revs, _ := s.Revisions(ctx, it.ID); len(revs) != 3 {
		t.Fatalf("setting cap: %d", len(revs))
	}
}

func TestRestoreRevision(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "post", Title: "Original", Body: "<p>one</p>", Status: "draft"})
	in := InputFromItem(it)
	in.Title, in.Body, in.Status = "Changed", "<p>two</p>", "published"
	it, _ = s.Update(ctx, it.ID, in)
	revs, _ := s.Revisions(ctx, it.ID)
	first := revs[len(revs)-1]
	if first.Snapshot.Title != "Original" {
		t.Fatalf("oldest: %+v", first.Snapshot)
	}
	restored, err := s.Restore(ctx, it.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != "Original" || restored.Body != "<p>one</p>" || restored.Status != "published" {
		t.Fatalf("restored: %+v", restored)
	}
	after, _ := s.Revisions(ctx, it.ID)
	if len(after) != len(revs)+2 || after[1].Snapshot.Title != "Changed" {
		t.Fatalf("restore must snapshot the current state first: %d revs, [1]=%q", len(after), after[1].Snapshot.Title)
	}
	if _, err := s.Restore(ctx, it.ID, "nope"); err != ErrNotFound {
		t.Fatalf("missing revision: %v", err)
	}
}
