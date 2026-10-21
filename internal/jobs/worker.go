package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const maxBackoff = 300 * time.Second

// backoff is min(2^attempts, 300) seconds.
func backoff(attempts int) time.Duration {
	if attempts >= 9 { // 2^9 = 512 > 300; also avoids overflow
		return maxBackoff
	}
	return time.Duration(math.Pow(2, float64(attempts))) * time.Second
}

// Start requeues jobs abandoned by a previous process (see recoverStale),
// then launches workers goroutines and the cron scheduler, which repeats the
// recovery on every tick. It returns immediately; cancel ctx to stop and call
// Wait to block until every goroutine has exited.
func (q *Queue) Start(ctx context.Context, workers int) {
	if workers < 1 {
		workers = 1
	}
	q.recover(ctx)
	host, _ := os.Hostname()
	for i := 0; i < workers; i++ {
		id := fmt.Sprintf("%s-%d-w%d", host, os.Getpid(), i)
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			q.work(ctx, id)
		}()
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.schedule(ctx)
	}()
}

// Wait blocks until all goroutines started by Start have exited.
func (q *Queue) Wait() { q.wg.Wait() }

// recover runs recoverStale and logs its outcome.
func (q *Queue) recover(ctx context.Context) {
	n, err := q.recoverStale(ctx)
	if err != nil {
		if ctx.Err() == nil {
			q.log.Error("jobs: recover stale", "err", err)
		}
		return
	}
	if n > 0 {
		q.log.Warn("jobs: requeued abandoned running jobs", "count", n, "stale_after", q.staleAfter.String())
	}
}

// recoverStale requeues running jobs whose worker stopped heartbeating: no
// locked_at, or locked_at older than staleAfter. A live worker refreshes
// locked_at every heartbeat interval, so jobs still being executed (by this
// or any other process) are left alone. Attempts are not counted.
func (q *Queue) recoverStale(ctx context.Context) (int64, error) {
	now := q.now()
	cutoff := db.FormatTime(now.Add(-q.staleAfter))
	res, err := q.db.ExecContext(ctx, `UPDATE jobs SET status = 'queued', locked_by = NULL, locked_at = NULL,
		updated_at = ? WHERE status = 'running' AND (locked_at IS NULL OR locked_at = '' OR locked_at <= ?)`,
		db.FormatTime(now), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// keepAlive refreshes locked_at of job id every heartbeat interval until
// done is closed, so recoverStale never mistakes it for abandoned.
func (q *Queue) keepAlive(ctx context.Context, id, workerID string, done <-chan struct{}) {
	t := time.NewTicker(q.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			now := db.FormatTime(q.now())
			if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET locked_at = ?, updated_at = ?
				WHERE id = ? AND status = 'running' AND locked_by = ?`, now, now, id, workerID); err != nil && ctx.Err() == nil {
				q.log.Warn("jobs: heartbeat", "id", id, "err", err)
			}
		}
	}
}

func (q *Queue) work(ctx context.Context, workerID string) {
	for ctx.Err() == nil {
		ran, err := q.runOnce(ctx, workerID)
		if err != nil && ctx.Err() == nil {
			q.log.Error("jobs: worker", "worker", workerID, "err", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(q.pollInterval):
		}
	}
}

// runOnce claims and runs one due job. It reports whether a job was claimed.
func (q *Queue) runOnce(ctx context.Context, workerID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, nil // shutting down: claim nothing new
	}
	now := db.FormatTime(q.now())
	var id, kind, payload string
	var attempts, maxAttempts int
	// Only kinds with a handler in this process are claimed: work of a
	// disabled or missing module waits in the queue instead of burning
	// attempts as "no handler registered". Parked jobs are never claimed.
	kinds := q.Kinds()
	if len(kinds) == 0 {
		return false, nil
	}
	args := []any{workerID, now, now}
	placeholders := make([]string, len(kinds))
	for i, k := range kinds {
		placeholders[i] = "?"
		args = append(args, k)
	}
	args = append(args, now)
	// The claim must not be interruptible: if ctx were cancelled after the
	// UPDATE committed but before RETURNING was scanned, the job would be
	// left 'running' and locked by this worker until recoverStale noticed.
	// Once claimed, the job is always requeued or finished below.
	err := q.db.QueryRowContext(context.WithoutCancel(ctx), `UPDATE jobs SET status = 'running', locked_by = ?, locked_at = ?, updated_at = ?
		WHERE id = (SELECT id FROM jobs WHERE status = 'queued' AND parked_reason = '' AND kind IN (`+strings.Join(placeholders, ",")+`)
			AND run_at <= ? ORDER BY run_at, id LIMIT 1)
		AND status = 'queued'
		RETURNING id, kind, payload_json, attempts, max_attempts`,
		args...).Scan(&id, &kind, &payload, &attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	done := make(chan struct{})
	beat := make(chan struct{})
	go func() {
		defer close(beat)
		q.keepAlive(ctx, id, workerID, done)
	}()
	runErr := q.execute(ContextWithAttempt(ctx, attempts+1, maxAttempts), kind, json.RawMessage(payload))
	close(done)
	<-beat
	// Record the outcome even though ctx may be cancelled by now.
	wctx := context.WithoutCancel(ctx)
	if runErr != nil && ctx.Err() != nil {
		return true, q.requeue(wctx, id) // shutdown, not a job failure
	}
	return true, q.finish(wctx, id, kind, attempts, maxAttempts, runErr)
}

type attemptKey struct{}

type attemptInfo struct{ attempt, max int }

// ContextWithAttempt returns ctx carrying attempt/max for
// AttemptFromContext. The worker sets it for every handler run; tests and
// code that invoke a handler directly can set it themselves.
func ContextWithAttempt(ctx context.Context, attempt, max int) context.Context {
	return context.WithValue(ctx, attemptKey{}, attemptInfo{attempt, max})
}

// AttemptFromContext returns, inside a job handler, the 1-based number of
// the attempt being run and the job's max attempts (attempt == max is the
// final attempt). Outside a handler it returns 0, 0.
func AttemptFromContext(ctx context.Context) (attempt, max int) {
	if a, ok := ctx.Value(attemptKey{}).(attemptInfo); ok {
		return a.attempt, a.max
	}
	return 0, 0
}

func (q *Queue) execute(ctx context.Context, kind string, payload json.RawMessage) (err error) {
	q.mu.RLock()
	reg, ok := q.handlers[kind]
	q.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no handler registered for kind %q", kind)
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return reg.handler(ctx, payload)
}

// requeue returns a job interrupted by shutdown to the queue, due now,
// without counting an attempt.
func (q *Queue) requeue(ctx context.Context, id string) error {
	now := db.FormatTime(q.now())
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status = 'queued', run_at = ?, locked_by = NULL,
		locked_at = NULL, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func (q *Queue) finish(ctx context.Context, id, kind string, attempts, maxAttempts int, runErr error) error {
	now := q.now()
	if runErr == nil {
		_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status = 'done', locked_by = NULL, locked_at = NULL,
			updated_at = ? WHERE id = ?`, db.FormatTime(now), id)
		return err
	}
	attempts++
	status, runAt := "queued", now.Add(backoff(attempts))
	if attempts >= maxAttempts {
		status = "dead"
	}
	q.log.Warn("jobs: attempt failed", "id", id, "kind", kind, "attempts", attempts, "status", status, "err", runErr)
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = ?, last_error = ?, run_at = ?,
		locked_by = NULL, locked_at = NULL, updated_at = ? WHERE id = ?`,
		status, attempts, runErr.Error(), db.FormatTime(runAt), db.FormatTime(now), id)
	return err
}

// RunOne claims and runs one due job on the caller's goroutine, reporting
// whether a job ran. It is for tests and one-shot tools that drive the
// queue without Start; a live worker process should not call it.
func (q *Queue) RunOne(ctx context.Context) (bool, error) {
	host, _ := os.Hostname()
	return q.runOnce(ctx, fmt.Sprintf("%s-%d-inline", host, os.Getpid()))
}
