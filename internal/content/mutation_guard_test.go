package content

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func TestGuardedSaveRejectsStaleDraftWithoutSideEffects(t *testing.T) {
	s := newTestService(t)
	it := mustCreate(t, s, Input{Type: "post", Title: "Original"})
	var auditBefore, revisionsBefore int
	if err := s.db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM revisions`).Scan(&revisionsBefore); err != nil {
		t.Fatal(err)
	}
	saved := false
	s.hooks.Action("content.after_save", 0, func(context.Context, any) error { saved = true; return nil })
	s.hooks.Filter("content.before_save", 0, func(_ context.Context, v any) (any, error) {
		_, err := s.db.Exec(`UPDATE content SET title='Concurrent draft' WHERE id=?`, it.ID)
		return v, err
	})
	_, err := s.Update(GuardUnapprovedMutation(context.Background()), it.ID, Input{Title: "Stale write"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	current, err := s.Get(context.Background(), it.ID)
	if err != nil || current.Title != "Concurrent draft" || current.Status != "draft" {
		t.Fatalf("concurrent draft lost: %+v %v", current, err)
	}
	var audits, revisions int
	if err = s.db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if saved || audits != auditBefore || revisions != revisionsBefore {
		t.Fatalf("rejected save had side effects: hook=%v audit=%d/%d revisions=%d/%d", saved, audits, auditBefore, revisions, revisionsBefore)
	}
}

func TestGuardedTranslationChecksEveryAffectedMember(t *testing.T) {
	for _, status := range []Status{"published", "private", "scheduled"} {
		t.Run(string(status), func(t *testing.T) {
			s := newTestService(t)
			a := mustCreate(t, s, Input{Type: "post", Title: "Arabic", Locale: "ar"})
			b := mustCreate(t, s, Input{Type: "post", Title: "English", Locale: "en"})
			if err := s.LinkTranslation(context.Background(), a.ID, b.ID); err != nil {
				t.Fatal(err)
			}
			// Only the root snapshot is passed: the second member must be reread under the write lock.
			snapshot, err := s.Get(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE content SET status=? WHERE id=?`, status, b.ID); err != nil {
				t.Fatal(err)
			}
			ctx := GuardUnapprovedMutation(context.Background())
			err = db.InTx(ctx, s.db, func(tx *sql.Tx) error { return s.guardMutationTx(ctx, tx, true, snapshot) })
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("live group member accepted: %v", err)
			}
			if err = s.UnlinkTranslation(ctx, a.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("unlink accepted: %v", err)
			}
			current, err := s.Get(context.Background(), a.ID)
			if err != nil || current.TranslationGroup != snapshot.TranslationGroup {
				t.Fatalf("membership changed: %+v %v", current, err)
			}
		})
	}
}

func TestGuardedTranslationChecksHiddenMemberOwnership(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "post", Title: "Own", Locale: "ar"})
	b := mustCreate(t, s, Input{Type: "post", Title: "Other", Locale: "en"})
	if err := s.LinkTranslation(context.Background(), a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE content SET author_id=CASE id WHEN ? THEN 'owner' ELSE 'other' END`, a.ID); err != nil {
		t.Fatal(err)
	}
	ctx := GuardUnapprovedMutation(auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "author"}))
	if err := s.UnlinkTranslation(ctx, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("hidden member ownership bypass: %v", err)
	}
}

func TestUnmarkedLiveSaveRetainsNativeSemantics(t *testing.T) {
	s := newTestService(t)
	it := mustCreate(t, s, Input{Type: "post", Title: "Published", Status: "published"})
	in := InputFromItem(it)
	in.Title = "Native edit"
	got, err := s.Update(context.Background(), it.ID, in)
	if err != nil || got.Title != "Native edit" || got.Status != "published" {
		t.Fatalf("unmarked save changed: %+v %v", got, err)
	}
}

func TestGuardTransactionExcludesConcurrentWriter(t *testing.T) {
	s := newTestService(t)
	snapshot := mustCreate(t, s, Input{Type: "post", Title: "Draft"})
	ctx := GuardUnapprovedMutation(context.Background())
	competitor, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	if _, err = competitor.ExecContext(ctx, `PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	defer competitor.ExecContext(context.Background(), `PRAGMA busy_timeout=5000`)
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.guardMutationTx(ctx, tx, false, snapshot); err != nil {
			return err
		}
		// A separate pool connection cannot publish between the guard and its write.
		if _, err := competitor.ExecContext(ctx, `UPDATE content SET status='published' WHERE id=?`, snapshot.ID); err == nil {
			t.Fatal("concurrent writer entered guarded transaction")
		}
		_, err := tx.ExecContext(ctx, `UPDATE content SET title='Guarded draft' WHERE id=?`, snapshot.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(context.Background(), snapshot.ID)
	if err != nil || current.Title != "Guarded draft" || current.Status != "draft" {
		t.Fatalf("guarded commit: %+v %v", current, err)
	}
	// The identical publication succeeds once that transaction has committed.
	if _, err = competitor.ExecContext(ctx, `UPDATE content SET status='published' WHERE id=?`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(ctx, snapshot.ID, Input{Title: "Too late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("subsequent live update was not guarded: %v", err)
	}
}
