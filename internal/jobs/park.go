package jobs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// Parking keeps queued work of a disabled or unregistered handler without
// running it, failing it or deleting it. Workers only claim kinds that have
// a handler in this process, so a job of an unknown kind waits untouched;
// Park records why (for operators) and Unpark releases the rows unchanged
// when the handler is enabled again.

// ParkedCounts reports queued jobs that are parked, by kind.
type ParkedCounts map[string]int

// Park marks every queued job of kind as parked with reason. Running, done
// and dead jobs are not touched. It returns how many rows changed.
func (q *Queue) Park(ctx context.Context, kind, reason string) (int64, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "parked"
	}
	res, err := q.db.ExecContext(ctx, `UPDATE jobs SET parked_reason = ?, updated_at = ?
		WHERE kind = ? AND status = 'queued' AND parked_reason = ''`, reason, db.FormatTime(q.now()), kind)
	if err != nil {
		return 0, fmt.Errorf("jobs: park %s: %w", kind, err)
	}
	return res.RowsAffected()
}

// Unpark releases parked jobs of kind back to the queue, due now, with their
// payload, attempts and idempotency key unchanged.
func (q *Queue) Unpark(ctx context.Context, kind string) (int64, error) {
	now := db.FormatTime(q.now())
	res, err := q.db.ExecContext(ctx, `UPDATE jobs SET parked_reason = '', run_at = ?, updated_at = ?
		WHERE kind = ? AND status = 'queued' AND parked_reason <> ''`, now, now, kind)
	if err != nil {
		return 0, fmt.Errorf("jobs: unpark %s: %w", kind, err)
	}
	return res.RowsAffected()
}

// Parked counts parked queued jobs by kind.
func (q *Queue) Parked(ctx context.Context) (ParkedCounts, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT kind, count(*) FROM jobs WHERE status = 'queued' AND parked_reason <> '' GROUP BY kind`)
	if err != nil {
		return nil, fmt.Errorf("jobs: parked: %w", err)
	}
	defer rows.Close()
	out := ParkedCounts{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// Unregistered counts queued, unparked jobs whose kind has no handler in
// this process. They are never claimed; the count tells operators that a
// disabled or missing module left work waiting.
func (q *Queue) Unregistered(ctx context.Context) (ParkedCounts, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT kind, count(*) FROM jobs WHERE status = 'queued' AND parked_reason = '' GROUP BY kind`)
	if err != nil {
		return nil, fmt.Errorf("jobs: unregistered: %w", err)
	}
	defer rows.Close()
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := ParkedCounts{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		if _, ok := q.handlers[kind]; !ok {
			out[kind] = n
		}
	}
	return out, rows.Err()
}

// Kinds returns the registered handler kinds, sorted.
func (q *Queue) Kinds() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]string, 0, len(q.handlers))
	for k := range q.handlers {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
