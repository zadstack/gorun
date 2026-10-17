package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// I7: Prune removes done jobs after 7 days, dead after 30, keyed (idempotent)
// ones only after 90; queued/running jobs are never removed.
func TestPruneRetention(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	add := func(id, status, key string, age time.Duration) {
		t.Helper()
		var k any
		if key != "" {
			k = key
		}
		ts := db.FormatTime(now.Add(-age))
		if _, err := q.db.ExecContext(ctx, `INSERT INTO jobs (id, kind, status, run_at, created_at, updated_at, idempotency_key)
			VALUES (?, 'x', ?, ?, ?, ?, ?)`, id, status, ts, ts, ts, k); err != nil {
			t.Fatal(err)
		}
	}
	add("done-new", "done", "", 6*day)
	add("done-old", "done", "", 8*day)
	add("dead-new", "dead", "", 29*day)
	add("dead-old", "dead", "", 31*day)
	add("keyed-done-60", "done", "evt_1", 60*day)
	add("keyed-dead-60", "dead", "evt_2", 60*day)
	add("keyed-done-91", "done", "evt_3", 91*day)
	add("queued-old", "queued", "", 400*day)
	add("running-old", "running", "", 400*day)

	n, err := q.Prune(ctx, now, DefaultRetention)
	if err != nil || n != 3 {
		t.Fatalf("pruned %d (%v), want 3", n, err)
	}
	left := map[string]bool{}
	rows, _ := q.db.QueryContext(ctx, `SELECT id FROM jobs`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		left[id] = true
	}
	rows.Close()
	for _, id := range []string{"done-new", "dead-new", "keyed-done-60", "keyed-dead-60", "queued-old", "running-old"} {
		if !left[id] {
			t.Errorf("%s must be kept", id)
		}
	}
	for _, id := range []string{"done-old", "dead-old", "keyed-done-91"} {
		if left[id] {
			t.Errorf("%s must be pruned", id)
		}
	}
	if c, _ := q.Count(ctx); c != 6 {
		t.Fatalf("count = %d", c)
	}
}
