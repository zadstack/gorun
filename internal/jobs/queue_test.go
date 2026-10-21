package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func newQueue(t *testing.T) *Queue {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "jobs", Migrations()); err != nil {
		t.Fatal(err)
	}
	q := New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.pollInterval = 10 * time.Millisecond
	return q
}

// fakeClock pins q.now so backoff and run_at checks are deterministic.
func fakeClock(q *Queue, start time.Time) *time.Time {
	now := start
	q.now = func() time.Time { return now }
	return &now
}

func getJob(t *testing.T, q *Queue, id string) Job {
	t.Helper()
	all, err := q.List(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range all {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("job %s not found", id)
	return Job{}
}

func TestEnqueueAndWorkerRunsHandler(t *testing.T) {
	q := newQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 1)
	q.Register("greet", func(_ context.Context, p json.RawMessage) error {
		var v struct{ Name string }
		json.Unmarshal(p, &v)
		got <- v.Name
		return nil
	}, 3)
	id, err := q.Enqueue(ctx, "greet", map[string]string{"Name": "zad"})
	if err != nil || len(id) != 26 {
		t.Fatalf("%q %v", id, err)
	}
	q.Start(ctx, 2)
	select {
	case name := <-got:
		if name != "zad" {
			t.Fatal(name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not run")
	}
	cancel()
	q.Wait()
	if j := getJob(t, q, id); j.Status != "done" || j.Attempts != 0 {
		t.Fatalf("%+v", j)
	}
}

func TestFailureIncrementsAttemptsAndBacksOff(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	q.Register("flaky", func(context.Context, json.RawMessage) error { return errors.New("nope") }, 5)
	id, _ := q.Enqueue(ctx, "flaky", nil)
	if ran, err := q.runOnce(ctx, "w1"); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	j := getJob(t, q, id)
	wantRunAt := db.FormatTime(now.Add(2 * time.Second)) // min(2^1, 300)s
	if j.Status != "queued" || j.Attempts != 1 || j.LastError != "nope" || j.RunAt != wantRunAt {
		t.Fatalf("%+v want run_at %s", j, wantRunAt)
	}
	// Not due yet: nothing to claim.
	if ran, _ := q.runOnce(ctx, "w1"); ran {
		t.Fatal("job should wait for backoff")
	}
	*now = now.Add(2 * time.Second)
	q.runOnce(ctx, "w1")
	if j := getJob(t, q, id); j.Attempts != 2 || j.RunAt != db.FormatTime(now.Add(4*time.Second)) {
		t.Fatalf("%+v", j)
	}
	st, err := q.Stats(ctx)
	if err != nil || st.Failed != 1 || st.Queued != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestBackoffCappedAt300(t *testing.T) {
	if backoff(1) != 2*time.Second || backoff(8) != 256*time.Second || backoff(9) != 300*time.Second || backoff(40) != 300*time.Second {
		t.Fatal(backoff(1), backoff(8), backoff(9), backoff(40))
	}
}

func TestIdempotencyKeyDedupes(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a, err := q.Enqueue(ctx, "k", 1, Idempotency("order-1"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := q.Enqueue(ctx, "k", 2, Idempotency("order-1"))
	c, _ := q.Enqueue(ctx, "k", 3) // no key: never deduped
	d, _ := q.Enqueue(ctx, "k", 4)
	if a != b || c == d {
		t.Fatalf("a=%s b=%s c=%s d=%s", a, b, c, d)
	}
	all, _ := q.List(ctx, "", 10)
	if len(all) != 3 {
		t.Fatalf("want 3 rows got %d", len(all))
	}
}

func TestDeadAfterMaxAttemptsAndRetry(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	var calls atomic.Int32
	q.Register("doomed", func(context.Context, json.RawMessage) error { calls.Add(1); return errors.New("x") }, 2)
	id, _ := q.Enqueue(ctx, "doomed", nil)
	q.runOnce(ctx, "w")
	*now = now.Add(time.Hour)
	q.runOnce(ctx, "w")
	if j := getJob(t, q, id); j.Status != "dead" || j.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("%+v calls=%d", j, calls.Load())
	}
	*now = now.Add(time.Hour)
	if ran, _ := q.runOnce(ctx, "w"); ran {
		t.Fatal("dead job must not run")
	}
	if err := q.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if j := getJob(t, q, id); j.Status != "queued" || j.Attempts != 0 {
		t.Fatalf("%+v", j)
	}
	if err := q.Retry(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound got %v", err)
	}
	dead, _ := q.List(ctx, "dead", 10)
	if len(dead) != 0 {
		t.Fatal("retried job still listed as dead")
	}
}

func TestRunAtDelaysJob(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	q.Register("later", func(context.Context, json.RawMessage) error { return nil }, 1)
	q.Enqueue(ctx, "later", nil, RunAt(now.Add(time.Minute)))
	if ran, _ := q.runOnce(ctx, "w"); ran {
		t.Fatal("ran before run_at")
	}
	*now = now.Add(time.Minute)
	if ran, _ := q.runOnce(ctx, "w"); !ran {
		t.Fatal("did not run at run_at")
	}
}

func TestPanicCountsAsFailureAndMissingHandlerWaits(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Register("panics", func(context.Context, json.RawMessage) error { panic("kaboom") }, 3)
	p, _ := q.Enqueue(ctx, "panics", nil)
	m, _ := q.Enqueue(ctx, "unregistered", nil)
	q.runOnce(ctx, "w")
	if ran, _ := q.runOnce(ctx, "w"); ran {
		t.Fatal("claimed a job without a handler")
	}
	if j := getJob(t, q, p); j.Attempts != 1 || j.LastError == "" {
		t.Fatalf("%+v", j)
	}
	// A disabled or missing module's work waits untouched: no attempt, no error.
	if j := getJob(t, q, m); j.Status != "queued" || j.Attempts != 0 || j.LastError != "" {
		t.Fatalf("%+v", j)
	}
	waiting, err := q.Unregistered(ctx)
	if err != nil || waiting["unregistered"] != 1 {
		t.Fatalf("unregistered=%v err=%v", waiting, err)
	}
	// Registering the handler later runs the preserved job as-is.
	q.Register("unregistered", func(context.Context, json.RawMessage) error { return nil }, 1)
	if ran, _ := q.runOnce(ctx, "w"); !ran {
		t.Fatal("registered handler did not claim the waiting job")
	}
	if j := getJob(t, q, m); j.Status != "done" {
		t.Fatalf("%+v", j)
	}
}

func TestParkHoldsQueuedWorkAndUnparkReleasesItUnchanged(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	var runs atomic.Int32
	q.Register("probe", func(context.Context, json.RawMessage) error { runs.Add(1); return nil }, 2)
	id, _ := q.Enqueue(ctx, "probe", map[string]string{"profile": "a"}, Idempotency("probe:a:1"))
	q.db.Exec(`UPDATE jobs SET attempts = 1 WHERE id = ?`, id)
	if n, err := q.Park(ctx, "probe", "module disabled"); err != nil || n != 1 {
		t.Fatalf("park n=%d err=%v", n, err)
	}
	if ran, _ := q.runOnce(ctx, "w"); ran || runs.Load() != 0 {
		t.Fatal("parked job was claimed")
	}
	st, _ := q.Stats(ctx)
	if st.Parked != 1 || st.Queued != 0 || st.Failed != 0 {
		t.Fatalf("stats %+v", st)
	}
	parked, _ := q.Parked(ctx)
	if parked["probe"] != 1 {
		t.Fatalf("parked %v", parked)
	}
	if j := getJob(t, q, id); j.ParkedReason != "module disabled" || j.Attempts != 1 || j.IdempotencyKey != "probe:a:1" {
		t.Fatalf("%+v", j)
	}
	// Parking twice is a no-op; an in-flight lookup still sees the job.
	if n, _ := q.Park(ctx, "probe", "again"); n != 0 {
		t.Fatal("re-parked")
	}
	if jid, ok, _ := q.InFlight(ctx, "probe:a:1"); !ok || jid != id {
		t.Fatal("parked job must stay in flight for deduplication")
	}
	if n, err := q.Unpark(ctx, "probe"); err != nil || n != 1 {
		t.Fatalf("unpark n=%d err=%v", n, err)
	}
	if ran, _ := q.runOnce(ctx, "w"); !ran || runs.Load() != 1 {
		t.Fatal("released job did not run")
	}
	j, err := q.Get(ctx, id)
	if err != nil || j.Status != "done" || j.ParkedReason != "" || j.Attempts != 1 {
		t.Fatalf("%+v %v", j, err)
	}
	if _, ok, _ := q.InFlight(ctx, "probe:a:1"); ok {
		t.Fatal("done job reported in flight")
	}
	if _, err := q.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestStartRequeuesStaleRunningJobs(t *testing.T) {
	q := newQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	id, _ := q.Enqueue(ctx, "k", nil)
	q.db.Exec(`UPDATE jobs SET status='running', locked_by='old-process' WHERE id=?`, id)
	q.Start(ctx, 1) // no handler registered: the job fails once and is requeued with backoff
	cancel()
	q.Wait()
	if j := getJob(t, q, id); j.Status != "queued" || j.LockedBy != "" {
		t.Fatalf("%+v", j)
	}
}

// cancelAfterCheck is cancelled by its first Err call (runOnce's shutdown
// check, which still sees nil), so the cancellation deterministically lands
// while the job is being claimed.
type cancelAfterCheck struct {
	context.Context
	once sync.Once
	done chan struct{}
}

func (c *cancelAfterCheck) Done() <-chan struct{} { return c.done }
func (c *cancelAfterCheck) Err() error {
	first := false
	c.once.Do(func() { first = true; close(c.done) })
	if first {
		return nil
	}
	return context.Canceled
}

func TestClaimRacingShutdownNeverLeavesJobRunning(t *testing.T) {
	q := newQueue(t)
	var ran atomic.Bool
	q.Register("k", func(hctx context.Context, _ json.RawMessage) error {
		ran.Store(true)
		return hctx.Err()
	}, 3)
	id, _ := q.Enqueue(context.Background(), "k", nil)
	ctx := &cancelAfterCheck{Context: context.Background(), done: make(chan struct{})}
	claimed, err := q.runOnce(ctx, "w")
	if err != nil || !claimed || !ran.Load() {
		t.Fatalf("claimed=%v ran=%v err=%v", claimed, ran.Load(), err)
	}
	if j := getJob(t, q, id); j.Status != "queued" || j.LockedBy != "" || j.Attempts != 0 {
		t.Fatalf("%+v", j)
	}
}

func TestCronRunsWhenIntervalElapsed(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	var runs atomic.Int32
	q.Cron("publish_due", time.Minute, func(context.Context) error { runs.Add(1); return nil })
	q.tick(ctx) // first tick always runs
	q.tick(ctx) // interval not elapsed
	*now = now.Add(time.Minute)
	q.tick(ctx)
	if runs.Load() != 2 {
		t.Fatalf("want 2 runs got %d", runs.Load())
	}
}

func TestStatsCountsByStatus(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Register("ok", func(context.Context, json.RawMessage) error { return nil }, 1)
	q.Enqueue(ctx, "ok", nil)
	q.Enqueue(ctx, "ok", nil)
	q.runOnce(ctx, "w")
	st, err := q.Stats(ctx)
	if err != nil || st.Queued != 1 || st.Done != 1 || st.Running != 0 || st.Dead != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestShutdownRequeuesWithoutCountingAttempt(t *testing.T) {
	q := newQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	q.Register("long", func(hctx context.Context, _ json.RawMessage) error {
		cancel() // shutdown arrives while the handler runs
		<-hctx.Done()
		return hctx.Err()
	}, 1)
	id, _ := q.Enqueue(context.Background(), "long", nil)
	if ran, err := q.runOnce(ctx, "w"); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if j := getJob(t, q, id); j.Status != "queued" || j.Attempts != 0 || j.LockedBy != "" {
		t.Fatalf("want requeued without attempt, got %+v", j)
	}
}

func TestAttemptFromContext(t *testing.T) {
	if a, m := AttemptFromContext(context.Background()); a != 0 || m != 0 {
		t.Fatalf("outside a job: %d/%d", a, m)
	}
	q := newQueue(t)
	ctx := context.Background()
	now := fakeClock(q, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	var seen []string
	q.Register("count", func(ctx context.Context, _ json.RawMessage) error {
		a, m := AttemptFromContext(ctx)
		seen = append(seen, fmt.Sprintf("%d/%d", a, m))
		return errors.New("again")
	}, 3)
	q.Enqueue(ctx, "count", nil)
	for i := 0; i < 3; i++ {
		q.runOnce(ctx, "w")
		*now = now.Add(time.Hour)
	}
	if strings.Join(seen, ",") != "1/3,2/3,3/3" {
		t.Fatalf("attempts seen by the handler: %v", seen)
	}
	if a, m := AttemptFromContext(ContextWithAttempt(ctx, 2, 5)); a != 2 || m != 5 {
		t.Fatalf("ContextWithAttempt: %d/%d", a, m)
	}
}
