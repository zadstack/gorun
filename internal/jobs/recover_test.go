package jobs

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// markRunning puts job id in 'running' as if worker owned it, with locked_at
// (empty means NULL).
func markRunning(t *testing.T, q *Queue, id, owner, lockedAt string) {
	t.Helper()
	var at any
	if lockedAt != "" {
		at = lockedAt
	}
	if _, err := q.db.Exec(`UPDATE jobs SET status='running', locked_by=?, locked_at=? WHERE id=?`, owner, at, id); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverStaleRequeuesOnlyAbandonedJobs(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	ids := map[string]string{}
	for _, name := range []string{"no_lock_time", "stale", "at_threshold", "fresh", "queued"} {
		id, err := q.Enqueue(ctx, "k", nil, Idempotency(name))
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	markRunning(t, q, ids["no_lock_time"], "old-process", "")
	markRunning(t, q, ids["stale"], "old-process", db.FormatTime(now.Add(-10*time.Minute)))
	markRunning(t, q, ids["at_threshold"], "old-process", db.FormatTime(now.Add(-q.staleAfter)))
	markRunning(t, q, ids["fresh"], "live-process", db.FormatTime(now.Add(-10*time.Second)))

	n, err := q.recoverStale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 requeued, got %d", n)
	}
	for _, name := range []string{"no_lock_time", "stale", "at_threshold"} {
		if j := getJob(t, q, ids[name]); j.Status != "queued" || j.LockedBy != "" || j.LockedAt != "" || j.Attempts != 0 {
			t.Fatalf("%s not requeued cleanly: %+v", name, j)
		}
	}
	if j := getJob(t, q, ids["fresh"]); j.Status != "running" || j.LockedBy != "live-process" {
		t.Fatalf("fresh running job must be left to its worker: %+v", j)
	}
	if j := getJob(t, q, ids["queued"]); j.Status != "queued" {
		t.Fatalf("queued job changed: %+v", j)
	}
}

func TestStartLeavesAnotherWorkersFreshJobRunning(t *testing.T) {
	q := newQueue(t)
	q.tickInterval = 10 * time.Millisecond // recovery also runs on every tick
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Int32
	q.Register("k", func(context.Context, json.RawMessage) error { ran.Add(1); return nil }, 1)
	id, err := q.Enqueue(ctx, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	markRunning(t, q, id, "live-process", db.FormatTime(time.Now()))
	q.Start(ctx, 1)
	time.Sleep(100 * time.Millisecond)
	cancel()
	q.Wait()
	if j := getJob(t, q, id); j.Status != "running" || j.LockedBy != "live-process" {
		t.Fatalf("startup recovery stole a job another worker is running: %+v", j)
	}
	if ran.Load() != 0 {
		t.Fatalf("job executed twice: %d", ran.Load())
	}
}

func TestSchedulerRecoversJobThatBecomesStaleAfterStart(t *testing.T) {
	q := newQueue(t)
	q.tickInterval = 10 * time.Millisecond
	var clock atomic.Int64
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock.Store(start.UnixNano())
	q.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); q.Wait() }()
	done := make(chan struct{})
	q.Register("k", func(context.Context, json.RawMessage) error { close(done); return nil }, 1)
	id, err := q.Enqueue(ctx, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A crash a moment ago: locked_at is still fresh at startup.
	markRunning(t, q, id, "crashed-process", db.FormatTime(start.Add(-5*time.Second)))
	q.Start(ctx, 1)
	time.Sleep(50 * time.Millisecond)
	if j := getJob(t, q, id); j.Status != "running" {
		t.Fatalf("fresh job requeued at startup: %+v", j)
	}
	clock.Store(start.Add(q.staleAfter).UnixNano()) // no heartbeat arrives
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("stale job not recovered by the scheduler: %+v", getJob(t, q, id))
	}
}

func TestHeartbeatKeepsLongRunningJobFresh(t *testing.T) {
	q := newQueue(t)
	q.heartbeat = 10 * time.Millisecond
	var clock atomic.Int64
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock.Store(start.UnixNano())
	q.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); q.Wait() }()
	running, release := make(chan struct{}), make(chan struct{})
	q.Register("long", func(context.Context, json.RawMessage) error {
		close(running)
		<-release
		return nil
	}, 1)
	id, err := q.Enqueue(ctx, "long", nil)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(ctx, 1)
	<-running
	later := start.Add(10 * time.Minute)
	clock.Store(later.UnixNano())
	deadline := time.Now().Add(3 * time.Second)
	for getJob(t, q, id).LockedAt != db.FormatTime(later) {
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat did not refresh locked_at: %+v", getJob(t, q, id))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n, err := q.recoverStale(ctx); err != nil || n != 0 {
		t.Fatalf("a heartbeating job was treated as stale: n=%d err=%v", n, err)
	}
	close(release)
	deadline = time.Now().Add(3 * time.Second)
	for getJob(t, q, id).Status != "done" {
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %+v", getJob(t, q, id))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if j := getJob(t, q, id); j.LockedAt != "" || j.LockedBy != "" {
		t.Fatalf("finished job kept its lock: %+v", j)
	}
}
