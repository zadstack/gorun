package observe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

type harness struct {
	t     *testing.T
	s     *Service
	q     *jobs.Queue
	now   time.Time
	runs  atomic.Int32
	rev   atomic.Value // string
	fail  atomic.Value // error
	block chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "jobs", jobs.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "observe", Migrations()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &harness{t: t, q: jobs.New(d, log), now: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)}
	h.rev.Store("r1")
	h.s = New(d, h.q, log)
	h.s.now = func() time.Time { return h.now }
	return h
}

func (h *harness) register(spec Spec) {
	if spec.Kind == "" {
		spec.Kind = "probe"
	}
	if spec.Revision == nil {
		spec.Revision = func(context.Context, string) (string, error) { return h.rev.Load().(string), nil }
	}
	if spec.Run == nil {
		spec.Run = func(ctx context.Context, key, revision string) (Result, error) {
			h.runs.Add(1)
			if h.block != nil {
				select {
				case <-h.block:
				case <-ctx.Done():
					return Result{}, ctx.Err()
				}
			}
			if f, _ := h.fail.Load().(error); f != nil {
				return Result{}, f
			}
			return Result{Scope: "connectivity", Data: map[string]any{"models": 3, "revision": revision}}, nil
		}
	}
	h.s.Register(spec)
}

func (h *harness) drain() {
	h.t.Helper()
	for i := 0; i < 20; i++ {
		ran, err := h.q.RunOne(context.Background())
		if err != nil {
			h.t.Fatal(err)
		}
		if !ran {
			return
		}
	}
}

func (h *harness) get(key string) Observation {
	h.t.Helper()
	o, err := h.s.Get(context.Background(), "probe", key)
	if err != nil {
		h.t.Fatal(err)
	}
	return o
}

func TestRefreshReturnsAtOnceAndReadsServeSavedResults(t *testing.T) {
	h := newHarness(t)
	h.register(Spec{Freshness: 10 * time.Minute, Remediation: "Check the provider."})
	ctx := context.Background()
	if o := h.get("a"); o.Status != "never" || o.Attempt != nil || o.Saved != nil {
		t.Fatalf("cold read must not run anything: %+v", o)
	}
	ref, err := h.s.Refresh(ctx, "probe", "a")
	if err != nil || ref.JobID == "" || ref.Generation != 1 || ref.Deduped {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}
	if h.runs.Load() != 0 {
		t.Fatal("refresh ran the check in the request")
	}
	if o := h.get("a"); o.Status != "waiting" || o.Attempt == nil || o.Attempt.JobID != ref.JobID {
		t.Fatalf("%+v", o)
	}
	h.drain()
	o := h.get("a")
	if o.Status != "fresh" || o.Saved == nil || o.Saved.Revision != "r1" || o.Saved.Scope != "connectivity" || string(o.Saved.Data) != `{"models":3,"revision":"r1"}` || o.Saved.RevisionMatch == nil || !*o.Saved.RevisionMatch {
		t.Fatalf("%+v saved=%+v", o, o.Saved)
	}
	if o.Attempt.Status != "succeeded" || o.Attempt.Number != 1 || o.Attempt.DurationMS == nil {
		t.Fatalf("%+v", o.Attempt)
	}
	h.now = h.now.Add(11 * time.Minute)
	if o := h.get("a"); o.Status != "stale" || o.Saved.AgeSeconds != 660 {
		t.Fatalf("%+v", o)
	}
	// A newer failed attempt keeps the last success visible with its age and
	// is never reported as a success.
	h.fail.Store(errors.New("provider returned HTTP 503"))
	if _, err := h.s.Refresh(ctx, "probe", "a"); err != nil {
		t.Fatal(err)
	}
	h.drain()
	o = h.get("a")
	if o.Status != "failed" || o.Attempt.Status != "failed" || o.Attempt.Error != "provider returned HTTP 503" || o.Attempt.Remediation != "Check the provider." {
		t.Fatalf("%+v", o.Attempt)
	}
	if o.Saved == nil || o.Saved.At != "2026-10-22T10:00:00Z" || o.Saved.AgeSeconds != 660 {
		t.Fatalf("last success lost: %+v", o.Saved)
	}
	if o.Attempt.FinishedAt == nil || *o.Attempt.FinishedAt != "2026-10-22T10:11:00Z" {
		t.Fatalf("failure time missing: %+v", o.Attempt)
	}
}

func TestConcurrentRefreshesAdmitOneJob(t *testing.T) {
	h := newHarness(t)
	h.register(Spec{})
	ctx := context.Background()
	var wg sync.WaitGroup
	refs := make([]Ref, 100)
	for i := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := h.s.Refresh(ctx, "probe", "a")
			if err != nil {
				t.Error(err)
			}
			refs[i] = r
		}()
	}
	wg.Wait()
	deduped := 0
	for _, r := range refs {
		if r.JobID != refs[0].JobID || r.Generation != 1 {
			t.Fatalf("second job admitted: %+v", r)
		}
		if r.Deduped {
			deduped++
		}
	}
	if deduped != 99 {
		t.Fatalf("deduplicated %d", deduped)
	}
	list, _ := h.q.List(ctx, "", 10)
	if len(list) != 1 {
		t.Fatalf("%d jobs", len(list))
	}
	h.drain()
	if h.runs.Load() != 1 {
		t.Fatalf("ran %d times", h.runs.Load())
	}
	// Once finished, a new refresh is admitted with the next generation.
	r, _ := h.s.Refresh(ctx, "probe", "a")
	if r.Deduped || r.Generation != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestConfigurationEditsRejectLateResults(t *testing.T) {
	h := newHarness(t)
	h.block = make(chan struct{})
	h.register(Spec{})
	ctx := context.Background()
	// Edited before the job starts: the attempt is superseded without running.
	if _, err := h.s.Refresh(ctx, "probe", "a"); err != nil {
		t.Fatal(err)
	}
	h.rev.Store("r2")
	close(h.block)
	h.drain()
	if o := h.get("a"); o.Status != "stale" || o.Attempt.Status != "superseded" || o.Attempt.Error != "configuration_changed" || o.Saved != nil || h.runs.Load() != 0 {
		t.Fatalf("%+v %+v", o, o.Attempt)
	}
	// Edited while the check is in flight: the result is discarded.
	h.block = make(chan struct{})
	if _, err := h.s.Refresh(ctx, "probe", "a"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.drain() }()
	for h.runs.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	h.rev.Store("r3")
	close(h.block)
	<-done
	if o := h.get("a"); o.Saved != nil || o.Attempt.Status != "superseded" {
		t.Fatalf("late result certified edited configuration: %+v", o)
	}
	// A refresh after an edit is not deduplicated against the superseded job.
	h.block = nil
	h.rev.Store("r4")
	first, _ := h.s.Refresh(ctx, "probe", "a")
	h.rev.Store("r5")
	second, _ := h.s.Refresh(ctx, "probe", "a")
	if second.Deduped || second.Generation != first.Generation+1 {
		t.Fatalf("edited configuration reused the stale refresh: %+v %+v", first, second)
	}
	h.drain()
	if o := h.get("a"); o.Status != "fresh" || o.Saved.Revision != "r5" {
		t.Fatalf("%+v", o)
	}
	// A removed key cannot be refreshed and is not found.
	h.rev.Store("")
	if _, err := h.s.Refresh(ctx, "probe", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := h.s.Get(ctx, "probe", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestTimeoutAndRetryExhaustionShowFailureWithTime(t *testing.T) {
	h := newHarness(t)
	h.block = make(chan struct{}) // never closed: every attempt times out
	h.register(Spec{Timeout: 20 * time.Millisecond, MaxAttempts: 2})
	ctx := context.Background()
	ref, err := h.s.Refresh(ctx, "probe", "a")
	if err != nil {
		t.Fatal(err)
	}
	// Attempts are spaced by the queue's backoff; run them directly.
	for i := 0; i < 2; i++ {
		h.q.Retry(ctx, ref.JobID)
		if ran, _ := h.q.RunOne(ctx); !ran {
			t.Fatal("attempt did not run")
		}
	}
	job, _ := h.q.Get(ctx, ref.JobID)
	o := h.get("a")
	if o.Status != "failed" || o.Attempt.Error != "timeout" || o.Attempt.FinishedAt == nil || o.Saved != nil {
		t.Fatalf("%+v job=%+v", o.Attempt, job)
	}
	if job.Status != "dead" && job.Status != "queued" {
		t.Fatalf("job %+v", job)
	}
}

func TestInterruptedRefreshRecoversAndCompletes(t *testing.T) {
	h := newHarness(t)
	h.register(Spec{})
	ctx := context.Background()
	ref, _ := h.s.Refresh(ctx, "probe", "a")
	// Simulate a process that died mid-attempt: the row says running, the job
	// is locked by a vanished worker. Queue recovery requeues it and the same
	// generation finishes the work.
	h.s.db.Exec(`UPDATE observations SET attempt_status = 'running', attempt_started_at = '2026-10-22T09:00:00Z' WHERE key = 'a'`)
	h.s.db.Exec(`UPDATE jobs SET status = 'running', locked_by = 'gone', locked_at = '2020-01-01T00:00:00Z' WHERE id = ?`, ref.JobID)
	if o := h.get("a"); o.Status != "running" {
		t.Fatalf("%+v", o)
	}
	wctx, cancel := context.WithCancel(ctx)
	h.q.Start(wctx, 1)
	deadline := time.Now().Add(5 * time.Second)
	for h.get("a").Status != "fresh" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	h.q.Wait()
	if o := h.get("a"); o.Status != "fresh" || o.Attempt.Generation != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestDisabledCapabilityIsNeitherScheduledNorRun(t *testing.T) {
	h := newHarness(t)
	var enabled atomic.Bool
	enabled.Store(true)
	h.register(Spec{Freshness: time.Minute, Keys: func(context.Context) ([]string, error) { return []string{"a", "b", "c"}, nil }, MaxInFlight: 2,
		Enabled: func(context.Context) bool { return enabled.Load() }})
	ctx := context.Background()
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := h.q.List(ctx, "", 10)
	if len(list) != 2 {
		t.Fatalf("MaxInFlight ignored: %d jobs", len(list))
	}
	h.drain()
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	h.drain()
	for _, k := range []string{"a", "b", "c"} {
		if o := h.get(k); o.Status != "fresh" {
			t.Fatalf("%s: %+v", k, o)
		}
	}
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.q.List(ctx, "queued", 10); len(l) != 0 {
		t.Fatal("fresh keys were rescheduled")
	}
	// Disabled: saved rows stay readable, nothing new runs, refresh is refused.
	enabled.Store(false)
	h.now = h.now.Add(time.Hour)
	runs := h.runs.Load()
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if h.runs.Load() != runs {
		t.Fatal("disabled capability ran")
	}
	if _, err := h.s.Refresh(ctx, "probe", "a"); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	o := h.get("a")
	if o.Status != "disabled" || o.Saved == nil || o.Enabled {
		t.Fatalf("%+v", o)
	}
	all, _ := h.s.All(ctx)
	if len(all) != 3 {
		t.Fatalf("rows lost: %d", len(all))
	}
}

func TestUnregisteredKindJobsAreParkedThenReleasedOnReenable(t *testing.T) {
	h := newHarness(t)
	h.register(Spec{})
	ctx := context.Background()
	ref, _ := h.s.Refresh(ctx, "probe", "a")
	// Next boot without this capability: a fresh service and queue on the
	// same database never claim the job and park it with its payload.
	h2 := &harness{t: t, q: jobs.New(h.s.db, h.s.log), now: h.now}
	h2.s = New(h.s.db, h2.q, h.s.log)
	if err := h2.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if ran, _ := h2.q.RunOne(ctx); ran {
		t.Fatal("claimed work of an absent capability")
	}
	job, _ := h2.q.Get(ctx, ref.JobID)
	if job.Status != "queued" || job.ParkedReason != "capability disabled" || job.Attempts != 0 {
		t.Fatalf("%+v", job)
	}
	// Re-enabled: the first tick releases the parked job and it completes.
	h3 := &harness{t: t, q: jobs.New(h.s.db, h.s.log), now: h.now}
	h3.rev.Store("r1")
	h3.s = New(h.s.db, h3.q, h.s.log)
	h3.s.now = func() time.Time { return h3.now }
	h3.register(Spec{})
	if err := h3.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	h3.drain()
	if o := h3.get("a"); o.Status != "fresh" {
		t.Fatalf("%+v", o)
	}
}

// Parking under live workers: a capability disabled while the queue runs
// keeps its queued refreshes parked (never claimed), and enabling it
// again releases them unchanged so they run.
func TestDisabledCapabilityParksQueuedWorkWhileWorkersRun(t *testing.T) {
	h := newHarness(t)
	var enabled atomic.Bool
	enabled.Store(true)
	h.register(Spec{Enabled: func(context.Context) bool { return enabled.Load() }})
	ctx := context.Background()
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Disable first, queue a refresh on the operator's behalf through the
	// scheduler path (Refresh itself refuses), then start workers.
	enabled.Store(false)
	enabled.Store(true)
	ref, _ := h.s.Refresh(ctx, "probe", "a")
	enabled.Store(false)
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	h.q.Start(wctx, 2)
	time.Sleep(150 * time.Millisecond)
	job, _ := h.q.Get(ctx, ref.JobID)
	if job.Status != "queued" || job.ParkedReason != "capability disabled" || h.runs.Load() != 0 {
		t.Fatalf("parked job ran or changed: %+v runs=%d", job, h.runs.Load())
	}
	if o := h.get("a"); o.Status != "disabled" {
		t.Fatalf("%+v", o)
	}
	enabled.Store(true)
	if err := h.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.get("a").Status != "fresh" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	h.q.Wait()
	job, _ = h.q.Get(ctx, ref.JobID)
	if job.Status != "done" || job.ParkedReason != "" || job.Attempts != 0 || h.runs.Load() != 1 {
		t.Fatalf("released job: %+v runs=%d", job, h.runs.Load())
	}
}
