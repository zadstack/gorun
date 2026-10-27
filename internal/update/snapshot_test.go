package update

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// acceptanceBetweenReads commits a real background lifecycle transition after
// the accepted-version read, before the active/last reads. No sleeps or timing
// assumptions are needed to reproduce the formerly mixed response.
type acceptanceBetweenReads struct {
	operationReader
	queries int
	accept  func()
}

func (r *acceptanceBetweenReads) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.queries++
	if r.queries == 2 {
		r.accept()
	}
	return r.operationReader.QueryRowContext(ctx, query, args...)
}

func TestStatusSnapshotDoesNotMixBackgroundAcceptance(t *testing.T) {
	for _, atomicSnapshot := range []bool{false, true} {
		name := "independent reads reproduce the bug"
		if atomicSnapshot {
			name = "read transaction preserves one committed state"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			s := w.boot("0.14.1").s
			ctx := context.Background()
			op, err := s.Request(ctx, Request{Version: "0.15.0", RequestedBy: "snapshot-test"})
			if err != nil {
				t.Fatal(err)
			}
			var reader operationReader = w.sql
			var tx *sql.Tx
			if atomicSnapshot {
				tx, err = w.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				reader = tx
			}
			interleaved := &acceptanceBetweenReads{operationReader: reader, accept: func() {
				writer, err := w.sql.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Rollback()
				now := db.FormatTime(time.Now())
				if _, err = writer.ExecContext(ctx, `UPDATE update_operations SET status = 'succeeded', activated_version = requested_version, accepted_version = requested_version, finished_at = ?, updated_at = ? WHERE id = ?`, now, now, op.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = writer.ExecContext(ctx, `UPDATE update_phases SET status = 'passed', finished_at = ?, updated_at = ? WHERE operation_id = ?`, now, now, op.ID); err != nil {
					t.Fatal(err)
				}
				if err = writer.Commit(); err != nil {
					t.Fatal(err)
				}
			}}
			snapshot, err := readStatusSnapshot(ctx, interleaved)
			if err != nil {
				t.Fatal(err)
			}
			if atomicSnapshot {
				if snapshot.AcceptedVersion != "" || snapshot.Active == nil || snapshot.Active.Status != StatusPending || snapshot.Last == nil || snapshot.Last.Status != StatusPending {
					t.Fatalf("mixed snapshot: %+v", snapshot)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else if snapshot.AcceptedVersion != "" || snapshot.Active != nil || snapshot.Last == nil || snapshot.Last.Status != StatusSucceeded {
				t.Fatalf("the deterministic interleaving did not reproduce the prior inconsistency: %+v", snapshot)
			}
			// A fresh public read sees the complete accepted state, not the old snapshot.
			fresh, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.AcceptedVersion != "0.15.0" || fresh.Active != nil || fresh.Last == nil || fresh.Last.Status != StatusSucceeded {
				t.Fatalf("bad fresh snapshot: %+v", fresh)
			}
		})
	}
}
