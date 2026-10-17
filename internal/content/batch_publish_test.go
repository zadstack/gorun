package content

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zadstack/gorun/internal/auth"
	"testing"
)

func TestPublishBatchRollsBackPagesAndCompanionSettings(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "page", Title: "First", Status: "draft"})
	b := mustCreate(t, s, Input{Type: "page", Title: "Second", Status: "draft"})
	err := s.PublishBatch(context.Background(), []PublishExpectation{{ID: a.ID, UpdatedAt: EditToken(a)}, {ID: b.ID, UpdatedAt: EditToken(b)}}, func(tx *sql.Tx, saved []*Item) error {
		if err := s.settings.SetTx(context.Background(), tx, "site.front_page", a.ID); err != nil {
			return err
		}
		return errors.New("activation failed")
	})
	if err == nil {
		t.Fatal("expected companion transaction failure")
	}
	for _, id := range []string{a.ID, b.ID} {
		it, err := s.Get(context.Background(), id)
		if err != nil || it.Status != "draft" {
			t.Fatalf("partial publish: %+v %v", it, err)
		}
	}
	if s.settings.GetString(context.Background(), "site.front_page", "") != "" {
		t.Fatal("partial settings activation")
	}
}

func TestPublishBatchChecksFreshExpectationsAndPublishesAtomically(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "page", Title: "First", Status: "draft"})
	if err := s.PublishBatch(context.Background(), []PublishExpectation{{ID: a.ID, UpdatedAt: "stale"}}, nil); !errors.Is(err, ErrPublishConflict) {
		t.Fatalf("stale: %v", err)
	}
	if err := s.PublishBatch(context.Background(), []PublishExpectation{{ID: a.ID, UpdatedAt: EditToken(a)}}, func(tx *sql.Tx, saved []*Item) error {
		return s.settings.SetTx(context.Background(), tx, "site.front_page", a.ID)
	}); err != nil {
		t.Fatal(err)
	}
	it, err := s.Get(context.Background(), a.ID)
	if err != nil || it.Status != "published" {
		t.Fatalf("publish: %+v %v", it, err)
	}
	revisions, err := s.Revisions(context.Background(), a.ID)
	if err != nil || len(revisions) < 2 {
		t.Fatalf("publish revision missing: %v", err)
	}
}

func TestPublishBatchRejectsAnotherAuthorsItemAndRollsBackEarlierPages(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "page", Title: "Own", Status: "draft", AuthorID: "author-a"})
	b := mustCreate(t, s, Input{Type: "page", Title: "Other", Status: "draft", AuthorID: "author-b"})
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "author-a", Role: "author", Status: auth.StatusActive})
	err := s.PublishBatch(ctx, []PublishExpectation{{ID: a.ID, UpdatedAt: EditToken(a)}, {ID: b.ID, UpdatedAt: EditToken(b)}}, nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ownership bypass: %v", err)
	}
	fresh, err := s.Get(context.Background(), a.ID)
	if err != nil || fresh.Status != "draft" {
		t.Fatalf("earlier page partially published: %+v %v", fresh, err)
	}
}
