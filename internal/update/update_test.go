package update

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// fakeSource is a trusted release index in memory.
type fakeSource struct {
	releases map[string]Artifact
	latest   string
	err      error
}

func (f *fakeSource) Name() string { return "fake-source" }
func (f *fakeSource) Resolve(_ context.Context, v string) (Artifact, error) {
	if f.err != nil {
		return Artifact{}, f.err
	}
	a, ok := f.releases[normalizeVersion(v)]
	if !ok {
		return Artifact{}, errors.New("release version not found in the trusted source")
	}
	return a, nil
}
func (f *fakeSource) Latest(ctx context.Context) (Artifact, error) { return f.Resolve(ctx, f.latest) }

// fakeAdapter records activation requests and answers with a scripted status.
type fakeAdapter struct {
	mu          sync.Mutex
	activations []Artifact
	status      Activation
	activateErr error
}

func (f *fakeAdapter) Name() string { return "fake-adapter" }
func (f *fakeAdapter) Activate(_ context.Context, a Artifact) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.activateErr != nil {
		return "", f.activateErr
	}
	f.activations = append(f.activations, a)
	return "dep-" + a.Version, nil
}
func (f *fakeAdapter) Status(context.Context, string) (Activation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeAdapter) setStatus(a Activation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = a
}
func (f *fakeAdapter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.activations)
}

// host is the evidence the admin module would provide.
type host struct {
	compat     Report
	compatErr  error
	backup     Report
	backupErr  error
	health     Report
	linkErr    error
	migrations atomic.Value // []string
	backups    atomic.Int32
}

func newHost() *host {
	h := &host{compat: Report{OK: true, Data: map[string]any{"theme": "default"}}, backup: Report{OK: true, Data: map[string]any{"stamp": "20261022T100000Z"}},
		health: Report{OK: true, Data: map[string]any{"ready": true}}}
	h.migrations.Store([]string{"content/0001_content.sql"})
	return h
}

func (h *host) config(running string, src Source, ad Adapter) Config {
	return Config{RunningVersion: running, Source: src, Adapter: ad, RequireDigest: true, ActivationTimeout: 1500 * time.Millisecond, PollInterval: 5 * time.Millisecond,
		Heartbeat: 10 * time.Millisecond, LeaseTTL: 5 * time.Second, HealthLinks: []string{"https://example.test/", "https://example.test/media/logo.png"},
		Compatibility: func(context.Context, string) (Report, error) { return h.compat, h.compatErr },
		Backup: func(context.Context) (Report, error) {
			if h.backupErr != nil {
				return Report{}, h.backupErr
			}
			h.backups.Add(1)
			return h.backup, nil
		},
		Health: func(context.Context) (Report, error) { return h.health, nil },
		Link:   func(context.Context, string) error { return h.linkErr },
		Schema: func(context.Context) ([]string, error) { return h.migrations.Load().([]string), nil },
	}
}

type world struct {
	t   *testing.T
	sql *sql.DB
	log *slog.Logger
	src *fakeSource
	ad  *fakeAdapter
	h   *host
}

func (w *world) sqlDB() *sql.DB { return w.sql }
func (w *world) db2() *sql.DB   { return w.sql }

func newWorld(t *testing.T) *world {
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
	if err := db.Migrate(ctx, d, "update", Migrations()); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{latest: "0.15.0", releases: map[string]Artifact{
		"0.14.1": {Version: "0.14.1", ID: "ghcr.io/zadstack/gorun:0.14.1", Digest: "sha256:" + strings.Repeat("a", 64), Source: "fake-source"},
		"0.15.0": {Version: "0.15.0", ID: "ghcr.io/zadstack/gorun:0.15.0", Digest: "sha256:" + strings.Repeat("b", 64), Source: "fake-source"},
		"0.16.0": {Version: "0.16.0", ID: "ghcr.io/zadstack/gorun:0.16.0", Source: "fake-source"},
	}}
	ad := &fakeAdapter{status: Activation{Status: ActivationQueued}}
	return &world{t: t, sql: d, log: slog.New(slog.NewTextHandler(io.Discard, nil)), src: src, ad: ad, h: newHost()}
}

// process is one booted GoRun process: its own queue and service on the shared database.
type process struct {
	q *jobs.Queue
	s *Service
}

func (w *world) boot(running string) *process {
	q := jobs.New(w.sqlDB(), w.log)
	s := New(w.sqlDB(), q, w.log, w.h.config(running, w.src, w.ad))
	s.Register()
	return &process{q: q, s: s}
}

// drain runs queued jobs inline until none is due or the deadline passes.
func (p *process) drain(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ran, err := p.q.RunOne(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			return
		}
	}
	t.Fatal("queue did not drain")
}

func phaseOf(op Operation, name string) Phase {
	for _, p := range op.Phases {
		if p.Name == name {
			return p
		}
	}
	return Phase{Name: name, Status: "missing"}
}

func TestCompleteUpdateAcrossProcessRestart(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	old := w.boot("0.14.1")
	op, err := old.s.Request(ctx, Request{Version: "v0.15.0", RequestedBy: "admin@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != StatusPending || len(op.Phases) != 6 || op.RunningVersion != "0.14.1" || op.AcceptedVersion != nil || op.ActivatedVersion != nil {
		t.Fatalf("%+v", op)
	}
	// The old process runs until activation waits on the provider, then is
	// shut down (ctx cancelled) as the provider restarts it.
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			got, _ := old.s.Get(ctx, op.ID)
			if phaseOf(got, PhaseActivation).Status == PhaseRunning && got.DeploymentRef != "" {
				cancel()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	if _, err := old.q.RunOne(runCtx); err != nil {
		t.Fatal(err)
	}
	got, _ := old.s.Get(ctx, op.ID)
	for _, name := range []string{PhaseCompatibility, PhaseVerification, PhaseBackup} {
		if p := phaseOf(got, name); p.Status != PhasePassed || p.DurationMS == nil || p.FinishedAt == nil {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	if got.Status != StatusRunning || got.DeploymentRef != "dep-0.15.0" || got.BackupStamp == "" || got.Artifact == nil || got.Previous == nil || got.Previous.Version != "0.14.1" {
		t.Fatalf("%+v", got)
	}
	if w.ad.count() != 1 || w.h.backups.Load() != 1 {
		t.Fatalf("activations=%d backups=%d", w.ad.count(), w.h.backups.Load())
	}
	// The accepted deployment is not success: nothing is activated or accepted yet.
	if got.ActivatedVersion != nil || got.AcceptedVersion != nil {
		t.Fatalf("queued deployment reported as activation: %+v", got)
	}
	// A conflicting request while the operation is in flight is refused.
	if _, err := old.s.Request(ctx, Request{Version: "0.16.0"}); err == nil || !strings.Contains(err.Error(), op.ID) {
		t.Fatalf("conflict not reported: %v", err)
	}
	// New process serving the target identity reconciles and finishes.
	w.h.migrations.Store([]string{"content/0001_content.sql", "content/0002_new.sql"})
	neu := w.boot("0.15.0")
	if err := neu.s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	got, _ = neu.s.Get(ctx, op.ID)
	if p := phaseOf(got, PhaseActivation); p.Status != PhasePassed || !strings.Contains(string(p.Result), "serving_identity_after_restart") {
		t.Fatalf("%+v", p)
	}
	if got.ActivatedVersion == nil || *got.ActivatedVersion != "0.15.0" || got.AcceptedVersion != nil {
		t.Fatalf("%+v", got)
	}
	neu.drain(t)
	got, _ = neu.s.Get(ctx, op.ID)
	if got.Status != StatusSucceeded || got.AcceptedVersion == nil || *got.AcceptedVersion != "0.15.0" || got.FinishedAt == nil || got.MigrationState != MigrationApplied {
		t.Fatalf("%+v", got)
	}
	for _, name := range []string{PhaseHealth, PhaseAcceptance} {
		if p := phaseOf(got, name); p.Status != PhasePassed {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	if !strings.Contains(string(phaseOf(got, PhaseHealth).Result), `"links":[{"ok":true`) {
		t.Fatalf("links not recorded: %s", phaseOf(got, PhaseHealth).Result)
	}
	if w.ad.count() != 1 {
		t.Fatal("activation was dispatched twice")
	}
	if _, held, _ := neu.s.Lease().Current(ctx); held {
		t.Fatal("lease not released")
	}
	if v, _ := neu.s.Accepted(ctx); v != "0.15.0" {
		t.Fatal(v)
	}
	// The recovered old job (requeued by the queue) finds a terminal operation and does nothing.
	neu.q.Retry(ctx, mustStartJob(t, neu.q, op.ID))
	neu.drain(t)
	if got, _ = neu.s.Get(ctx, op.ID); got.Status != StatusSucceeded || w.ad.count() != 1 {
		t.Fatalf("%+v", got)
	}
}

func mustStartJob(t *testing.T, q *jobs.Queue, opID string) string {
	t.Helper()
	list, _ := q.List(context.Background(), "", 100)
	for _, j := range list {
		if j.IdempotencyKey == "update:"+opID+":start" {
			return j.ID
		}
	}
	t.Fatal("start job missing")
	return ""
}

func TestPhaseFailuresStopBeforeSideEffects(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(w *world)
		version string
		phase   string
		reason  string
		backups int32
		retry   bool
	}{
		{"incompatible theme", func(w *world) {
			w.h.compat = Report{Reason: "theme not supported", Remediation: "Pick a supported theme."}
		}, "0.15.0", PhaseCompatibility, "theme not supported", 0, false},
		{"development build", func(w *world) {}, "0.15.0", PhaseCompatibility, "not a release version", 0, false},
		{"downgrade", func(w *world) {}, "0.14.1", PhaseCompatibility, "downgrade requires review", 0, false},
		{"unknown target", func(w *world) {}, "0.99.0", PhaseVerification, "release could not be resolved", 0, true},
		{"missing digest", func(w *world) {}, "0.16.0", PhaseVerification, "publishes no digest", 0, false},
		{"backup disk failure", func(w *world) { w.h.backupErr = errors.New("no space left on device") }, "0.15.0", PhaseBackup, "backup failed", 0, true},
		{"corrupt manifest", func(w *world) { w.h.backup = Report{Reason: "manifest checksum mismatch"} }, "0.15.0", PhaseBackup, "manifest checksum mismatch", 1, false},
		{"activation request refused", func(w *world) { w.ad.activateErr = errors.New("deployment provider returned HTTP 401") }, "0.15.0", PhaseActivation, "activation request failed", 1, true},
		{"provider failed", func(w *world) { w.ad.setStatus(Activation{Status: ActivationFailed}) }, "0.15.0", PhaseActivation, "activation failed", 1, true},
		{"dispatch timeout after acceptance", func(w *world) {}, "0.15.0", PhaseActivation, "timed out after upstream acceptance", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tc.prepare(w)
			running := "0.14.1"
			if tc.name == "development build" {
				running = "v0.14.1-3-gabc123-dirty"
			}
			if tc.name == "downgrade" {
				running = "0.15.0"
			}
			p := w.boot(running)
			op, err := p.s.Request(context.Background(), Request{Version: tc.version})
			if err != nil {
				t.Fatal(err)
			}
			p.drain(t)
			got, _ := p.s.Get(context.Background(), op.ID)
			ph := phaseOf(got, tc.phase)
			if got.Status != StatusFailed || ph.Status != PhaseFailed || !strings.Contains(ph.Error, tc.reason) || ph.Remediation == "" || got.Retryable != tc.retry {
				t.Fatalf("op=%+v phase=%+v", got, ph)
			}
			if w.h.backups.Load() != tc.backups {
				t.Fatalf("backups=%d", w.h.backups.Load())
			}
			if tc.phase != PhaseActivation && w.ad.count() != 0 {
				t.Fatal("activation requested after an earlier phase failed")
			}
			if got.AcceptedVersion != nil || got.ActivatedVersion != nil {
				t.Fatal("failed operation reported acceptance")
			}
			if _, held, _ := p.s.Lease().Current(context.Background()); held {
				t.Fatal("lease leaked")
			}
			// After a failure a new request is admitted again, once the
			// provider no longer reports an accepted deployment in progress.
			w.ad.setStatus(Activation{Status: ActivationFailed})
			if _, err := p.s.Request(context.Background(), Request{Version: "0.15.0"}); err != nil && tc.name != "downgrade" {
				t.Fatalf("new request refused: %v", err)
			}
		})
	}
}

func TestDigestMismatchAndDowngradeConsent(t *testing.T) {
	w := newWorld(t)
	p := w.boot("0.14.1")
	ctx := context.Background()
	if _, err := p.s.Request(ctx, Request{Version: "0.15.0", ExpectedDigest: "nonsense"}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatal(err)
	}
	op, err := p.s.Request(ctx, Request{Version: "0.15.0", ExpectedDigest: "sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	p.drain(t)
	got, _ := p.s.Get(ctx, op.ID)
	if ph := phaseOf(got, PhaseVerification); ph.Status != PhaseFailed || !strings.Contains(ph.Error, "digest does not match") || w.h.backups.Load() != 0 {
		t.Fatalf("%+v", ph)
	}
	// Downgrade with operator consent passes compatibility and records the constraint.
	w2 := newWorld(t)
	q := jobs.New(w2.sqlDB(), w2.log)
	cfg := w2.h.config("0.15.0", w2.src, w2.ad)
	cfg.AllowDowngrade = true
	s := New(w2.sqlDB(), q, w2.log, cfg)
	s.Register()
	op, err = s.Request(ctx, Request{Version: "0.14.1"})
	if err != nil {
		t.Fatal(err)
	}
	(&process{q: q, s: s}).drain(t)
	got, _ = s.Get(ctx, op.ID)
	if ph := phaseOf(got, PhaseCompatibility); ph.Status != PhasePassed || !strings.Contains(string(ph.Result), `"downgrade":true`) {
		t.Fatalf("%+v", ph)
	}
}

func TestHealthFailureRollsBackToPriorArtifactOnCurrentData(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	old := w.boot("0.14.1")
	op, _ := old.s.Request(ctx, Request{Version: "0.15.0"})
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			if got, _ := old.s.Get(ctx, op.ID); got.DeploymentRef != "" {
				cancel()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	old.q.RunOne(runCtx)
	// The new process is unhealthy (a broken media link); no migrations were applied.
	w.h.linkErr = errors.New("HTTP 404")
	neu := w.boot("0.15.0")
	if err := neu.s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	// The runner requests the prior artifact and waits; simulate the provider
	// restarting the old release by stopping this process mid-wait.
	runCtx2, cancel2 := context.WithCancel(ctx)
	go func() {
		for {
			if got, _ := neu.s.Get(ctx, op.ID); got.RollbackRef != "" {
				cancel2()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	for {
		ran, _ := neu.q.RunOne(runCtx2)
		if !ran || runCtx2.Err() != nil {
			break
		}
	}
	got, _ := neu.s.Get(ctx, op.ID)
	if got.Status != StatusRunning || phaseOf(got, PhaseHealth).Status != PhaseFailed || phaseOf(got, PhaseAcceptance).Status != PhaseSkipped || phaseOf(got, PhaseRollbackCompatibility).Status != PhasePassed || got.RollbackRef != "dep-0.14.1" {
		t.Fatalf("%+v", got)
	}
	if w.ad.count() != 2 || w.ad.activations[1].Version != "0.14.1" {
		t.Fatalf("rollback did not activate the prior artifact: %+v", w.ad.activations)
	}
	w.h.linkErr = nil
	back := w.boot("0.14.1")
	if err := back.s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	back.drain(t)
	got, _ = back.s.Get(ctx, op.ID)
	if got.Status != StatusRolledBack || phaseOf(got, PhaseRollbackHealth).Status != PhasePassed || got.ActivatedVersion == nil || *got.ActivatedVersion != "0.14.1" || got.AcceptedVersion != nil {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(got.Error, "link failed") || got.Remediation == "" {
		t.Fatalf("rollback must keep the original failure: %+v", got)
	}
	if v, _ := back.s.Accepted(ctx); v != "" {
		t.Fatal("rolled back update was accepted")
	}
}

func TestRollbackRefusedWhenActivationAppliedMigrations(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	old := w.boot("0.14.1")
	op, _ := old.s.Request(ctx, Request{Version: "0.15.0"})
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			if got, _ := old.s.Get(ctx, op.ID); got.DeploymentRef != "" {
				cancel()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	old.q.RunOne(runCtx)
	w.h.migrations.Store([]string{"content/0001_content.sql", "content/0002_new.sql"})
	w.h.health = Report{Reason: "readiness failed", Remediation: "Inspect the log."}
	neu := w.boot("0.15.0")
	neu.s.Reconcile(ctx, true)
	neu.drain(t)
	got, _ := neu.s.Get(ctx, op.ID)
	if got.Status != StatusRollbackFailed || got.MigrationState != MigrationApplied || w.ad.count() != 1 {
		t.Fatalf("rolled back over newer schema: %+v activations=%d", got, w.ad.count())
	}
	ph := phaseOf(got, PhaseRollbackCompatibility)
	if ph.Status != PhaseFailed || !strings.Contains(ph.Remediation, "No snapshot was restored") {
		t.Fatalf("%+v", ph)
	}
	if phaseOf(got, PhaseRollbackActivation).Status != PhasePending {
		t.Fatal("rollback activation ran despite uncertain compatibility")
	}
}

func TestHealthFailureWithoutPriorArtifactFailsWithGuidance(t *testing.T) {
	w := newWorld(t)
	delete(w.src.releases, "0.14.1")
	ctx := context.Background()
	old := w.boot("0.14.1")
	op, _ := old.s.Request(ctx, Request{Version: "0.15.0"})
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			if got, _ := old.s.Get(ctx, op.ID); got.DeploymentRef != "" {
				cancel()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	old.q.RunOne(runCtx)
	w.h.health = Report{Reason: "protected checks failed", Remediation: "See System Health."}
	neu := w.boot("0.15.0")
	neu.s.Reconcile(ctx, true)
	neu.drain(t)
	got, _ := neu.s.Get(ctx, op.ID)
	if got.Status != StatusFailed || got.Previous != nil || !strings.Contains(got.Remediation, "do not restore an old backup") || w.ad.count() != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRestartDuringEarlyPhaseIsInterruptedNotSuccessful(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.boot("0.14.1")
	op, _ := p.s.Request(ctx, Request{Version: "0.15.0"})
	// Crash while backup is running: rows say running, the runner is gone.
	w.db2().Exec(`UPDATE update_operations SET status = 'running', current_phase = 'backup', started_at = '2026-10-22T10:00:00Z', heartbeat_at = '2026-10-22T10:00:00Z' WHERE id = ?`, op.ID)
	w.db2().Exec(`UPDATE update_phases SET status = 'passed' WHERE operation_id = ? AND name IN ('compatibility', 'verification')`, op.ID)
	w.db2().Exec(`UPDATE update_phases SET status = 'running', started_at = '2026-10-22T10:00:00Z' WHERE operation_id = ? AND name = 'backup'`, op.ID)
	w.db2().Exec(`UPDATE jobs SET status = 'running', locked_by = 'gone', locked_at = '2020-01-01T00:00:00Z'`)
	p2 := w.boot("0.14.1")
	if err := p2.s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	got, _ := p2.s.Get(ctx, op.ID)
	if got.Status != StatusInterrupted || phaseOf(got, PhaseBackup).Status != PhaseInterrupted || got.FinishedAt == nil || !got.Retryable || got.Remediation == "" {
		t.Fatalf("%+v", got)
	}
	if _, held, _ := p2.s.Lease().Current(ctx); held {
		t.Fatal("lease not released on interruption")
	}
	// The recovered job finds a terminal operation; a new request is accepted.
	p2.q.Start(ctx, 1)
	time.Sleep(50 * time.Millisecond)
	if _, err := p2.s.Request(ctx, Request{Version: "0.15.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestReconcilesAcceptedDeploymentBeforeDispatching(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.boot("0.14.1")
	op, _ := p.s.Request(ctx, Request{Version: "0.15.0"})
	p.drain(t) // activation times out after the provider accepted it
	got, _ := p.s.Get(ctx, op.ID)
	if got.Status != StatusFailed || got.DeploymentRef == "" {
		t.Fatalf("%+v", got)
	}
	w.ad.setStatus(Activation{Status: ActivationRunning})
	_, err := p.s.Request(ctx, Request{Version: "0.15.0"})
	var conflict *ErrConflict
	if !errors.As(err, &conflict) || conflict.OperationID != op.ID {
		t.Fatalf("expected conflict on an in-progress accepted deployment: %v", err)
	}
	w.ad.setStatus(Activation{Status: ActivationFailed})
	if _, err := p.s.Request(ctx, Request{Version: "0.15.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseBlocksConflictingWorkAndFencesExpiredOwners(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.boot("0.14.1")
	l := p.s.Lease()
	if err := l.Acquire(ctx, HolderBackup, "backup-1", ""); err != nil {
		t.Fatal(err)
	}
	_, err := p.s.Request(ctx, Request{Version: "0.15.0"})
	var conflict *ErrConflict
	if !errors.As(err, &conflict) || conflict.Holder != HolderBackup {
		t.Fatalf("update started during a backup: %v", err)
	}
	if err := l.Acquire(ctx, HolderRestore, "restore-1", ""); err == nil {
		t.Fatal("restore started during a backup")
	}
	if err := l.Acquire(ctx, HolderBackup, "backup-1", ""); err != nil {
		t.Fatal("re-entrant acquire by the same owner must succeed")
	}
	// Expiry: a stale owner loses the lease and its renewals fail.
	w.db2().Exec(`UPDATE ops_leases SET expires_at = '2020-01-01T00:00:00Z'`)
	if err := l.Acquire(ctx, HolderUpdate, "update-1", "op"); err != nil {
		t.Fatal(err)
	}
	if err := l.Renew(ctx, "backup-1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale owner renewed: %v", err)
	}
	if err := l.Release(ctx, "backup-1"); err != nil {
		t.Fatal(err)
	}
	if h, held, _ := l.Current(ctx); !held || h.Owner != "update-1" {
		t.Fatalf("release by a non-owner removed the lease: %+v %v", h, held)
	}
	if err := l.Release(ctx, "update-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.s.Request(ctx, Request{Version: "0.15.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRequestsAdmitExactlyOneOperation(t *testing.T) {
	w := newWorld(t)
	p := w.boot("0.14.1")
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.s.Request(context.Background(), Request{Version: "0.15.0"}); err == nil {
				admitted.Add(1)
			} else {
				var conflict *ErrConflict
				if !errors.As(err, &conflict) {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d", admitted.Load())
	}
	list, _ := p.s.List(context.Background(), 10)
	if len(list) != 1 {
		t.Fatal(len(list))
	}
}

func TestRunnerStopsWhenLeaseIsStolen(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.boot("0.14.1")
	op, _ := p.s.Request(ctx, Request{Version: "0.15.0"})
	p.s.cfg.ActivationTimeout = 5 * time.Second
	done := make(chan error, 1)
	go func() { _, err := p.q.RunOne(ctx); done <- err }()
	for {
		if got, _ := p.s.Get(ctx, op.ID); got.DeploymentRef != "" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Another owner takes the lease (as Reconcile would after an expiry).
	w.db2().Exec(`UPDATE ops_leases SET owner = 'other', expires_at = '2999-01-01T00:00:00Z'`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fenced runner kept going")
	}
	got, _ := p.s.Get(ctx, op.ID)
	if got.Status != StatusRunning || phaseOf(got, PhaseActivation).Status != PhaseRunning {
		t.Fatalf("fenced runner changed the operation: %+v", got)
	}
}

func TestNotConfiguredAndInvalidTargets(t *testing.T) {
	w := newWorld(t)
	q := jobs.New(w.sqlDB(), w.log)
	s := New(w.sqlDB(), q, w.log, Config{RunningVersion: "0.14.1"})
	if _, err := s.Request(context.Background(), Request{Version: "0.15.0"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
	if s.AdapterName() != "manual" || s.Configured() {
		t.Fatal("manual adapter must be the default")
	}
	p := w.boot("0.14.1")
	for _, v := range []string{"", "latest", "0.15", "../x", "0.15.0; drop"} {
		if _, err := p.s.Request(context.Background(), Request{Version: v}); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("%q: %v", v, err)
		}
	}
	if _, err := p.s.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCoolifyAdapterNeverLeaksCredentialsAndRequiresResourceAcceptance(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			t.Error("missing bearer")
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/deploy":
			w.Write([]byte(`{"deployments":[{"resource_uuid":"site_123","deployment_uuid":"job123"}]}`))
		case r.Method == "GET" && r.URL.Path == "/api/v1/deployments/job123":
			w.Write([]byte(`{"status":"in_progress"}`))
		case r.Method == "GET" && r.URL.Path == "/api/v1/deployments/other":
			w.WriteHeader(401)
			w.Write([]byte(`secret upstream message with fake-token`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, bad := range []string{"http://coolify.example", "https://u:p@coolify.example", "https://coolify.example/path", "https://coolify.example?x=1"} {
		if _, err := NewCoolifyAdapter(bad, "fake-token", "site_123", nil); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := NewCoolifyAdapter(server.URL, "fake-token", "../x", nil); err == nil {
		t.Error("accepted bad resource")
	}
	ad, err := NewCoolifyAdapter(server.URL, "fake-token", "site_123", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ad.Activate(context.Background(), Artifact{Version: "0.15.0"})
	if err != nil || ref != "job123" {
		t.Fatalf("%q %v", ref, err)
	}
	st, _ := ad.Status(context.Background(), ref)
	if st.Status != ActivationRunning {
		t.Fatalf("%+v", st)
	}
	st, _ = ad.Status(context.Background(), "other")
	if st.Status != ActivationUnknown || strings.Contains(st.Detail, "fake-token") || strings.Contains(st.Detail, "secret upstream") {
		t.Fatalf("%+v", st)
	}
	st, _ = ad.Status(context.Background(), "../etc")
	if st.Status != ActivationUnknown {
		t.Fatalf("%+v", st)
	}
	// A provider that does not echo the configured resource is not accepted.
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"deployments":[{"resource_uuid":"other","deployment_uuid":"job9"}]}`))
	}))
	defer other.Close()
	ad2, _ := NewCoolifyAdapter(other.URL, "fake-token", "site_123", other.Client())
	if _, err := ad2.Activate(context.Background(), Artifact{}); err == nil {
		t.Fatal("accepted an unrelated deployment")
	}
}

func TestReleaseIndexResolvesVerifiedArtifacts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"latest":"0.15.0","releases":[
			{"version":"0.15.0","artifacts":[{"id":"ghcr.io/zadstack/gorun:0.15.0","digest":"sha256:` + strings.Repeat("b", 64) + `"},{"id":"gorun-0.15.0-linux-amd64.tar.gz","digest":"sha256:` + strings.Repeat("c", 64) + `"}]},
			{"version":"0.14.1","artifacts":[{"id":"ghcr.io/zadstack/gorun:0.14.1","digest":"bad"}]},
			{"version":"0.13.0","artifacts":[{"id":"only-one"}]}]}`))
	}))
	defer server.Close()
	if _, err := NewReleaseIndex("http://plain.example/index.json", "", "", nil); err == nil {
		t.Fatal("accepted http source")
	}
	src, err := NewReleaseIndex(server.URL+"/releases.json", "", "linux-amd64", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	a, err := src.Resolve(context.Background(), "v0.15.0")
	if err != nil || a.ID != "gorun-0.15.0-linux-amd64.tar.gz" || a.Version != "0.15.0" || !strings.HasPrefix(a.Digest, "sha256:c") {
		t.Fatalf("%+v %v", a, err)
	}
	if l, err := src.Latest(context.Background()); err != nil || l.Version != "0.15.0" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := src.Resolve(context.Background(), "0.14.1"); err == nil {
		t.Fatal("accepted a malformed digest")
	}
	if a, err := src.Resolve(context.Background(), "0.13.0"); err != nil || a.Digest != "" {
		t.Fatalf("%+v %v", a, err)
	}
	ambiguous, _ := NewReleaseIndex(server.URL+"/releases.json", "", "", server.Client())
	if _, err := ambiguous.Resolve(context.Background(), "0.15.0"); err == nil {
		t.Fatal("picked an artifact without a selector")
	}
	if _, err := src.Resolve(context.Background(), "0.99.0"); err == nil {
		t.Fatal("resolved an unknown version")
	}
}

func TestVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"0.15.0", "0.14.1", 1}, {"0.14.1", "0.14.1", 0}, {"0.14.1-rc.1", "0.14.1", -1}, {"1.0.0", "0.99.9", 1}} {
		av, _ := parseVersion(tc.a)
		bv, _ := parseVersion(tc.b)
		if got := av.compare(bv); got != tc.want {
			t.Errorf("%s vs %s = %d", tc.a, tc.b, got)
		}
	}
	for _, bad := range []string{"dev", "66a2646", "v0.14.1-3-gabc-dirty", "0.14"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}
