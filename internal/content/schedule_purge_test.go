package content

import (
	"context"
	"errors"
	"testing"
	"time"
)

// One item that cannot be deleted must not keep the rest of the expired
// trash from being purged.
func TestPurgeTrashContinuesPastAFailingItem(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"First", "Stuck", "Last"} {
		it := mustCreate(t, s, Input{Type: "post", Title: title})
		if err := s.Delete(ctx, it.ID, false); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
	}
	stuck := ids[1]
	if _, err := s.db.Exec(`CREATE TRIGGER block_purge BEFORE DELETE ON content WHEN OLD.id = '` + stuck + `' BEGIN SELECT RAISE(ABORT, 'locked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.purgeTrash(ctx, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatalf("purge tick: %v", err)
	}
	for _, id := range []string{ids[0], ids[2]} {
		if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("item %s after purge: %v", id, err)
		}
	}
	if _, err := s.Get(ctx, stuck); err != nil {
		t.Fatalf("the failing item stays in the trash: %v", err)
	}
}
