package observe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// handler returns the job handler of kind. Every write is fenced by the
// generation the job was queued with and by the configuration revision,
// so a late result cannot certify edited configuration, recreate a removed
// key or overwrite a newer refresh.
func (s *Service) handler(kind string) jobs.Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p refreshPayload
		if err := json.Unmarshal(raw, &p); err != nil || p.Kind != kind || p.Key == "" {
			return fmt.Errorf("observe: invalid refresh payload for %s", kind)
		}
		spec, ok := s.spec(kind)
		if !ok {
			return fmt.Errorf("observe: kind %s not registered", kind)
		}
		wctx := context.WithoutCancel(ctx)
		if !spec.enabled(ctx) {
			s.supersede(wctx, p, "capability_disabled")
			return nil
		}
		attempt, _ := jobs.AttemptFromContext(ctx)
		started := s.now()
		res, err := s.db.ExecContext(ctx, `UPDATE observations SET attempt_status = 'running', attempt_started_at = ?, attempt_finished_at = '', attempt_number = ?,
			attempt_error = '', attempt_remediation = '', attempt_duration_ms = NULL, updated_at = ? WHERE kind = ? AND key = ? AND generation = ?`,
			db.FormatTime(started), attempt, db.FormatTime(started), kind, p.Key, p.Generation)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // a newer generation owns the row
		}
		if rev, err := spec.Revision(ctx, p.Key); err != nil || rev != p.Revision {
			s.supersede(wctx, p, "configuration_changed")
			return nil
		}
		rctx, cancel := context.WithTimeout(ctx, spec.Timeout)
		result, runErr := spec.Run(rctx, p.Key, p.Revision)
		cancel()
		finished := s.now()
		duration := finished.Sub(started).Milliseconds()
		if runErr == nil {
			// Re-check after the run: an edit made while the check was in
			// flight must not be certified by a result for the old revision.
			if rev, err := spec.Revision(wctx, p.Key); err != nil || rev != p.Revision {
				s.supersede(wctx, p, "configuration_changed")
				return nil
			}
			data, err := json.Marshal(result.Data)
			if err != nil {
				runErr = fmt.Errorf("observe: result not serializable")
			} else {
				_, err = s.db.ExecContext(wctx, `UPDATE observations SET attempt_status = 'succeeded', attempt_finished_at = ?, attempt_duration_ms = ?,
					success_at = ?, success_revision = ?, success_duration_ms = ?, success_scope = ?, result_json = ?, updated_at = ?
					WHERE kind = ? AND key = ? AND generation = ? AND pending_revision = ?`,
					db.FormatTime(finished), duration, db.FormatTime(finished), p.Revision, duration, result.Scope, string(data), db.FormatTime(finished),
					kind, p.Key, p.Generation, p.Revision)
				return err
			}
		}
		if ctx.Err() != nil && errors.Is(runErr, context.Canceled) {
			return runErr // shutdown: the queue requeues without counting an attempt
		}
		msg := sanitizeError(runErr, rctx)
		_, err = s.db.ExecContext(wctx, `UPDATE observations SET attempt_status = 'failed', attempt_finished_at = ?, attempt_duration_ms = ?, attempt_error = ?,
			attempt_remediation = ?, updated_at = ? WHERE kind = ? AND key = ? AND generation = ?`,
			db.FormatTime(finished), duration, msg, spec.Remediation, db.FormatTime(finished), kind, p.Key, p.Generation)
		if err != nil {
			return err
		}
		return fmt.Errorf("observe: %s/%s: %s", kind, p.Key, msg)
	}
}

func (s *Service) supersede(ctx context.Context, p refreshPayload, reason string) {
	now := db.FormatTime(s.now())
	if _, err := s.db.ExecContext(ctx, `UPDATE observations SET attempt_status = 'superseded', attempt_finished_at = ?, attempt_error = ?, updated_at = ?
		WHERE kind = ? AND key = ? AND generation = ?`, now, reason, now, p.Kind, p.Key, p.Generation); err != nil {
		s.log.Warn("observe: supersede", "kind", p.Kind, "err", err)
	}
}

// sanitizeError keeps a short operator-readable reason. Checks are expected
// to return messages without credentials; the length cap limits what a
// verbose upstream error could leak.
func sanitizeError(err error, ctx context.Context) string {
	if err == nil {
		return "check failed"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := err.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen] + "…"
	}
	return msg
}

// Tick is the scheduler: run it from a queue cron. It parks queued
// refreshes of kinds no longer registered (a disabled capability), releases
// parked refreshes of registered kinds once, then admits a refresh for
// every key whose saved result is missing, stale or for another revision,
// up to MaxInFlight per kind. Disabled kinds are skipped entirely.
func (s *Service) Tick(ctx context.Context) error {
	s.once.Do(func() {
		for _, kind := range s.Kinds() {
			if n, err := s.queue.Unpark(ctx, JobKindPrefix+kind); err != nil {
				s.log.Warn("observe: unpark", "kind", kind, "err", err)
			} else if n > 0 {
				s.log.Info("observe: released parked refreshes", "kind", kind, "count", n)
			}
		}
	})
	waiting, err := s.queue.Unregistered(ctx)
	if err != nil {
		return err
	}
	for jobKind := range waiting {
		if len(jobKind) > len(JobKindPrefix) && jobKind[:len(JobKindPrefix)] == JobKindPrefix {
			if _, err := s.queue.Park(ctx, jobKind, "capability disabled"); err != nil {
				return err
			}
		}
	}
	var firstErr error
	for _, kind := range s.Kinds() {
		if err := s.tickKind(ctx, kind); err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return firstErr
}

func (s *Service) tickKind(ctx context.Context, kind string) error {
	spec, _ := s.spec(kind)
	if spec.Keys == nil || spec.Freshness <= 0 || !spec.enabled(ctx) {
		return nil
	}
	keys, err := spec.Keys(ctx)
	if err != nil {
		return fmt.Errorf("observe: keys %s: %w", kind, err)
	}
	var inFlight int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM observations o JOIN jobs j ON j.id = o.job_id
		WHERE o.kind = ? AND o.attempt_status IN ('waiting', 'running') AND j.status IN ('queued', 'running')`, kind).Scan(&inFlight); err != nil {
		return err
	}
	for _, key := range keys {
		if inFlight >= spec.MaxInFlight {
			return nil
		}
		o, err := s.Get(ctx, kind, key)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		switch o.Status {
		case "waiting", "running", "fresh", "disabled":
			continue
		}
		if o.Status == "failed" && o.Attempt != nil && o.Attempt.FinishedAt != nil {
			// Do not hammer a failing upstream: wait a freshness interval
			// after the last failed attempt before scheduling again.
			if t, err := db.ParseTime(*o.Attempt.FinishedAt); err == nil && s.now().Sub(t) < spec.Freshness {
				continue
			}
		}
		if _, err := s.Refresh(ctx, kind, key); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDisabled) {
			return err
		}
		inFlight++
	}
	return nil
}

// Forget deletes the saved observation of key (for a removed profile).
// Queued refreshes for it are superseded when they run.
func (s *Service) Forget(ctx context.Context, kind, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM observations WHERE kind = ? AND key = ?`, kind, key)
	return err
}

// Age formats a saved result's age for templates.
func Age(seconds int64) string { return (time.Duration(seconds) * time.Second).String() }

// Record saves the outcome of a check that ran synchronously (an explicit
// operator action) as the key's newest attempt, outside the job path. The
// generation is unchanged; a refresh queued for the same key keeps its own
// fence. A nil err saves the result as the last success; otherwise the
// previous success is kept and only the attempt is recorded.
func (s *Service) Record(ctx context.Context, kind, key, revision string, res Result, runErr error, duration time.Duration) error {
	spec, ok := s.spec(kind)
	if !ok {
		return ErrUnknownKind
	}
	now := db.FormatTime(s.now())
	ms := duration.Milliseconds()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO observations (kind, key, updated_at) VALUES (?, ?, ?) ON CONFLICT (kind, key) DO NOTHING`, kind, key, now); err != nil {
		return err
	}
	if runErr == nil {
		data, err := json.Marshal(res.Data)
		if err != nil {
			return fmt.Errorf("observe: result not serializable")
		}
		_, err = s.db.ExecContext(ctx, `UPDATE observations SET attempt_status = 'succeeded', attempt_started_at = ?, attempt_finished_at = ?, attempt_number = 1,
			attempt_error = '', attempt_remediation = '', attempt_duration_ms = ?, pending_revision = ?,
			success_at = ?, success_revision = ?, success_duration_ms = ?, success_scope = ?, result_json = ?, updated_at = ? WHERE kind = ? AND key = ?`,
			now, now, ms, revision, now, revision, ms, res.Scope, string(data), now, kind, key)
		return err
	}
	msg := sanitizeError(runErr, ctx)
	_, err := s.db.ExecContext(ctx, `UPDATE observations SET attempt_status = 'failed', attempt_started_at = ?, attempt_finished_at = ?, attempt_number = 1,
		attempt_error = ?, attempt_remediation = ?, attempt_duration_ms = ?, pending_revision = ?, updated_at = ? WHERE kind = ? AND key = ?`,
		now, now, msg, spec.Remediation, ms, revision, now, kind, key)
	return err
}
