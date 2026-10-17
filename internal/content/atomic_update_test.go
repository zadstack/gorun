package content

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAtomicUpdateConflictsAndRollsBackJoinedWrite(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	original := mustCreate(t, s, Input{Type: "page", Title: "Original", Body: "<p>Old site</p>", Status: "published"})
	frozen, _ := time.Parse(time.RFC3339, original.UpdatedAt)
	s.now = func() time.Time { return frozen }
	changed := InputFromItem(original)
	changed.Title = "Concurrent edit"
	current, err := s.Update(ctx, original.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	input := InputFromItem(original)
	input.Body = "<p>Builder content</p>"
	called := false
	_, err = s.UpdateAtomic(ctx, original.ID, input, EditToken(original), func(tx *sql.Tx, it *Item) error { called = true; return nil })
	if !errors.Is(err, ErrConflict) || called {
		t.Fatalf("stale write: err=%v called=%v", err, called)
	}
	before, _ := s.Revisions(ctx, original.ID)
	failure := errors.New("draft update failed")
	_, err = s.UpdateAtomic(ctx, original.ID, input, EditToken(current), func(tx *sql.Tx, it *Item) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	after, _ := s.Revisions(ctx, original.ID)
	stored, _ := s.Get(ctx, original.ID)
	if stored.Body != current.Body || stored.Title != current.Title || len(before) != len(after) {
		t.Fatal("failed joined write changed content or revisions")
	}
	_, err = s.UpdateAtomic(ctx, original.ID, input, EditToken(current), func(tx *sql.Tx, it *Item) error {
		var body string
		if err := tx.QueryRowContext(ctx, "SELECT body FROM content WHERE id=?", it.ID).Scan(&body); err != nil {
			return err
		}
		if body != input.Body {
			t.Errorf("callback did not observe newly written body: %s", body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	revisions, _ := s.Revisions(ctx, original.ID)
	found := false
	for _, revision := range revisions {
		if revision.Snapshot.Title == current.Title && revision.Snapshot.Body == current.Body {
			found = true
		}
	}
	if !found {
		t.Fatal("pre-builder state missing from revision history")
	}
}
