package content

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// Two concurrent saves, A under B and B under A, each validate against the
// other's pre-commit state. The save transaction re-walks the parent chain,
// so the second commit is refused instead of creating a cycle.
func TestParentCycleRecheckedInsideSaveTransaction(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mustCreate(t, s, Input{Type: "page", Title: "A"})
	b := mustCreate(t, s, Input{Type: "page", Title: "B"})

	in := InputFromItem(b)
	in.ParentID = a.ID
	// B's save passes its pre-transaction check (A has no parent yet); the
	// concurrent save of A under B commits before B's write transaction.
	_, err := s.save(ctx, b, in, saveOpts{inTx: func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE content SET parent_id = ? WHERE id = ?`, b.ID, a.ID)
		return err
	}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["parent_id"] != "content.validation.parent" {
		t.Fatalf("cyclic save: %v", err)
	}
	got, err := s.Get(ctx, b.ID)
	if err != nil || got.ParentID != "" {
		t.Fatalf("B after the refused save: %+v %v", got, err)
	}
}
