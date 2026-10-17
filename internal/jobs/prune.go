package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Retention says how long finished jobs are kept, by their last update
// (updated_at, the time they finished). Keyed applies to jobs with an
// idempotency key: they guard against duplicates (e.g. a webhook event id),
// so they are kept for the longer of Keyed and their status window.
type Retention struct {
	Done, Dead, Keyed time.Duration
}

// DefaultRetention keeps done jobs 7 days, dead jobs 30 days and jobs with
// an idempotency key 90 days.
var DefaultRetention = Retention{Done: 7 * 24 * time.Hour, Dead: 30 * 24 * time.Hour, Keyed: 90 * 24 * time.Hour}

// Prune deletes done and dead jobs older than r (relative to now) and
// returns how many rows it removed. Queued, running and failed jobs are
// never touched.
func (q *Queue) Prune(ctx context.Context, now time.Time, r Retention) (int64, error) {
	cut := func(d time.Duration) string { return db.FormatTime(now.Add(-d)) }
	res, err := q.db.ExecContext(ctx, `DELETE FROM jobs WHERE
		(status = 'done' AND idempotency_key IS NULL AND updated_at < ?) OR
		(status = 'dead' AND idempotency_key IS NULL AND updated_at < ?) OR
		(status = 'done' AND idempotency_key IS NOT NULL AND updated_at < ?) OR
		(status = 'dead' AND idempotency_key IS NOT NULL AND updated_at < ?)`,
		cut(r.Done), cut(r.Dead), cut(max(r.Keyed, r.Done)), cut(max(r.Keyed, r.Dead)))
	if err != nil {
		return 0, fmt.Errorf("jobs: prune: %w", err)
	}
	return res.RowsAffected()
}

// Count returns the number of rows in the jobs table.
func (q *Queue) Count(ctx context.Context) (int, error) {
	var n int
	if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil {
		return 0, fmt.Errorf("jobs: count: %w", err)
	}
	return n, nil
}
