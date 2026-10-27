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
				for _, phase := range snapshot.Last.Phases {
					if phase.Status != PhasePending {
						t.Fatalf("phase escaped the read snapshot: %+v", phase)
					}
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
			for _, phase := range fresh.Last.Phases {
				if phase.Status != PhasePassed {
					t.Fatalf("fresh phase did not include committed acceptance: %+v", phase)
				}
			}
		})
	}
}

func TestStatusSnapshotPreservesEarlierAcceptanceWithLaterOperation(t *testing.T) {
	for _, status := range []string{StatusPending, StatusRunning, StatusFailed} {
		t.Run(status, func(t *testing.T) {
			w := newWorld(t)
			s := w.boot("0.14.1").s
			ctx := context.Background()
			first, err := s.Request(ctx, Request{Version: "0.15.0"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.sql.ExecContext(ctx, `UPDATE update_operations SET status = 'succeeded', accepted_version = requested_version, finished_at = ?, created_at = ? WHERE id = ?`, db.FormatTime(time.Now().Add(-time.Minute)), db.FormatTime(time.Now().Add(-time.Hour)), first.ID); err != nil {
				t.Fatal(err)
			}
			holder, held, err := s.Lease().Current(ctx)
			if err != nil || !held {
				t.Fatalf("expected a held test lease: %v", err)
			}
			if err := s.Lease().Release(ctx, holder.Owner); err != nil {
				t.Fatal(err)
			}
			later, err := s.Request(ctx, Request{Version: "0.15.0"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.sql.ExecContext(ctx, `UPDATE update_operations SET status = ? WHERE id = ?`, status, later.ID); err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.AcceptedVersion != "0.15.0" || snapshot.Last == nil || snapshot.Last.ID != later.ID || snapshot.Last.Status != status {
				t.Fatalf("lost historical acceptance: %+v", snapshot)
			}
			if status == StatusFailed {
				if snapshot.Active != nil {
					t.Fatal("failed operation reported as active")
				}
			} else if snapshot.Active == nil || snapshot.Active.ID != later.ID {
				t.Fatal("pending/running operation not reported as active")
			}
		})
	}
}
