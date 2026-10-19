package content

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func TestDraftWithProvenanceRollsBackEverything(t *testing.T) {
	s := newDraftTestService(t)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "reviewer", Role: "admin", Status: auth.StatusActive})
	stopped := errors.New("provenance failed")
	saved := false
	s.hooks.Action("content.after_save", 0, func(context.Context, any) error { saved = true; return nil })
	_, e := s.CreateDraftWith(ctx, Input{Type: "post", Title: "New", Status: "published"}, func(tx *sql.Tx, it *Item) error {
		if it.Status != "draft" || it.AuthorID != "reviewer" {
			t.Fatal("unsafe callback", it)
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM content WHERE id=?`, it.ID).Scan(&n); err != nil || n != 1 {
			t.Fatal("not same transaction", n, err)
		}
		return stopped
	})
	if !errors.Is(e, stopped) || saved {
		t.Fatalf("callback rollback %v saved=%v", e, saved)
	}
	for _, table := range []string{"content", "revisions", "audit_log"} {
		var n int
		if e = s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatalf("orphan %s %d %v", table, n, e)
		}
	}
}
func TestDraftWithProvenanceCannotPublishThroughFilter(t *testing.T) {
	s := newDraftTestService(t)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "reviewer", Role: "admin", Status: auth.StatusActive})
	s.hooks.Filter("content.before_save", 0, func(_ context.Context, v any) (any, error) { in := v.(*Input); in.Status = "published"; return in, nil })
	if _, e := s.CreateDraftWith(ctx, Input{Type: "post", Title: "New"}, nil); !errors.Is(e, ErrConflict) {
		t.Fatal("filter publication accepted", e)
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM content`).Scan(&n); e != nil || n != 0 {
		t.Fatal("published orphan", n, e)
	}
}
func TestDraftWithProvenanceRequiresActualCreatePermission(t *testing.T) {
	s := newDraftTestService(t)
	for _, ctx := range []context.Context{context.Background(), auth.WithUser(context.Background(), &auth.User{ID: "viewer", Role: "customer", Status: auth.StatusActive}), auth.WithUser(context.Background(), &auth.User{ID: "admin", Role: "admin", Status: "disabled"})} {
		if _, e := s.CreateDraftWith(ctx, Input{Type: "post"}, nil); !errors.Is(e, ErrForbidden) {
			t.Fatal("missing permission accepted", e)
		}
	}
}

func newDraftTestService(t *testing.T) *service {
	s := newTestService(t)
	if e := db.Migrate(context.Background(), s.db, "auth", auth.New().Migrations()); e != nil {
		t.Fatal(e)
	}
	if e := auth.ReloadRoles(context.Background(), s.db); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`INSERT INTO users(id,email,role,created_at,updated_at) VALUES('reviewer','reviewer@fixture.test','admin','','')`); e != nil {
		t.Fatal(e)
	}
	return s
}

func TestDraftWithProvenanceRechecksRevokedPermissionInsideTransaction(t *testing.T) {
	s := newDraftTestService(t)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "reviewer", Role: "admin", Status: auth.StatusActive})
	s.hooks.Filter("content.before_save", 0, func(_ context.Context, v any) (any, error) {
		_, err := s.db.Exec(`DELETE FROM role_permissions WHERE role='admin'`)
		return v, err
	})
	if _, e := s.CreateDraftWith(ctx, Input{Type: "post", Title: "Unapproved"}, nil); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked permission accepted", e)
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM content`).Scan(&n); e != nil || n != 0 {
		t.Fatal("unauthorized draft persisted", n, e)
	}
}
