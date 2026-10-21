// Package observe keeps durable, revision-fenced observations of expensive
// or external checks so pages and APIs serve the last saved result at once
// while refreshes run as bounded background jobs.
//
// Each registered Spec describes one capability kind: how to enumerate its
// keys, the configuration revision a key currently has, whether the
// capability is enabled, and the check to run. A refresh is admitted at
// most once per in-flight generation (one hundred concurrent requests make
// one job), runs with a timeout and a bounded number of attempts, and only
// writes its result when the configuration revision it was queued for is
// still current. The last successful result stays visible with its age when
// a newer attempt fails; it is never relabelled as the newer attempt.
package observe

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the observations table migrations.
func Migrations() fs.FS { return migrations }

// ServiceName is the kernel service name the admin module publishes.
const ServiceName = "observe"

// JobKindPrefix prefixes the job kind of every capability ("observe.<kind>"),
// so a disabled capability's queued refreshes wait unclaimed (and can be
// parked) instead of failing as unregistered work.
const JobKindPrefix = "observe."

const (
	defaultTimeout     = 20 * time.Second
	defaultMaxAttempts = 3
	defaultFreshness   = 15 * time.Minute
	defaultMaxInFlight = 2
	maxErrorLen        = 300
)

// Errors returned by Refresh.
var (
	ErrUnknownKind = errors.New("observe: unknown capability kind")
	ErrDisabled    = errors.New("observe: capability disabled")
	ErrNotFound    = errors.New("observe: key not found")
)

// NotFound lets api.Fail map ErrNotFound to 404.
func (e notFoundErr) NotFound() bool { return true }

type notFoundErr struct{ error }

func (e notFoundErr) Unwrap() error { return e.error }

// Result is what a check returns. Scope names the evidence level, e.g.
// "connectivity" or "discovery"; it is saved beside the data so readers
// never mistake reachability for a verified operation.
type Result struct {
	Scope string
	Data  any
}

// Spec registers one capability kind.
type Spec struct {
	Kind string
	// Timeout bounds one attempt (default 20s). MaxAttempts bounds retries
	// through the job queue (default 3). Freshness is how long a success is
	// served as fresh and how often the scheduler refreshes keys (default
	// 15m; <= 0 means refresh only on demand). MaxInFlight caps queued or
	// running refreshes of this kind admitted by the scheduler (default 2).
	Timeout     time.Duration
	MaxAttempts int
	Freshness   time.Duration
	MaxInFlight int
	// Keys enumerates the keys the scheduler refreshes (nil: on demand only).
	Keys func(ctx context.Context) ([]string, error)
	// Revision returns the key's current configuration revision, or "" when
	// the key no longer exists. A result is only saved while the revision it
	// was queued for is still current.
	Revision func(ctx context.Context, key string) (string, error)
	// Enabled reports whether the capability may run (nil: always). A
	// disabled capability is neither scheduled nor refreshed on demand;
	// its saved rows are kept.
	Enabled func(ctx context.Context) bool
	// Run performs the check for key at revision. Its error message is
	// stored (truncated) and shown to operators: never include credentials.
	Run func(ctx context.Context, key, revision string) (Result, error)
	// Remediation is the operator hint stored with a failed attempt.
	Remediation string
}

func (s Spec) jobKind() string { return JobKindPrefix + s.Kind }

func (s Spec) enabled(ctx context.Context) bool { return s.Enabled == nil || s.Enabled(ctx) }

// Attempt is the latest refresh attempt, independent from the saved result.
type Attempt struct {
	Status      string  `json:"status" enum:"waiting,running,succeeded,failed,superseded"`
	StartedAt   *string `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	Number      int     `json:"number" doc:"1-based attempt number within the job; 0 while waiting"`
	DurationMS  *int64  `json:"duration_ms" doc:"Null until the attempt finished"`
	Error       string  `json:"error" doc:"Sanitized failure reason; empty on success"`
	Remediation string  `json:"remediation"`
	JobID       string  `json:"job_id"`
	Generation  int64   `json:"generation"`
}

// Saved is the last successful read, kept even when newer attempts fail.
type Saved struct {
	At            string          `json:"at"`
	AgeSeconds    int64           `json:"age_seconds"`
	Revision      string          `json:"revision"`
	RevisionMatch *bool           `json:"revision_match" doc:"Null when the key's current revision cannot be read"`
	DurationMS    *int64          `json:"duration_ms"`
	Scope         string          `json:"scope"`
	Data          json.RawMessage `json:"data"`
}

// Observation is the readable state of one key.
type Observation struct {
	Kind    string   `json:"kind"`
	Key     string   `json:"key"`
	Status  string   `json:"status" enum:"never,waiting,running,fresh,stale,failed,disabled" doc:"never: no attempt; failed: the newest attempt failed (saved keeps the last success); stale: saved is older than freshness or for another revision"`
	Attempt *Attempt `json:"attempt" doc:"Null before the first refresh"`
	Saved   *Saved   `json:"saved" doc:"Null until a refresh succeeded"`
	Enabled bool     `json:"enabled"`
}

// Ref identifies an admitted or deduplicated refresh.
type Ref struct {
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	JobID      string `json:"job_id"`
	Generation int64  `json:"generation"`
	Deduped    bool   `json:"deduplicated" doc:"True when a refresh for this key was already queued or running"`
}

// Service owns the observations table and the refresh jobs.
type Service struct {
	db    *sql.DB
	queue *jobs.Queue
	log   *slog.Logger
	now   func() time.Time

	mu    sync.RWMutex
	specs map[string]Spec
	once  sync.Once
}

// New returns a service on d; q must be the queue whose workers will run
// the refreshes. Register specs before the queue starts.
func New(d *sql.DB, q *jobs.Queue, log *slog.Logger) *Service {
	return &Service{db: d, queue: q, log: log, now: time.Now, specs: map[string]Spec{}}
}

// Register adds a capability kind and its job handler. Panics on an empty
// kind or a duplicate (a programmer error, like duplicate routes).
func (s *Service) Register(spec Spec) {
	if strings.TrimSpace(spec.Kind) == "" {
		panic("observe: empty kind")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = defaultTimeout
	}
	if spec.MaxAttempts <= 0 {
		spec.MaxAttempts = defaultMaxAttempts
	}
	if spec.Freshness == 0 {
		spec.Freshness = defaultFreshness
	}
	if spec.MaxInFlight <= 0 {
		spec.MaxInFlight = defaultMaxInFlight
	}
	s.mu.Lock()
	if _, dup := s.specs[spec.Kind]; dup {
		s.mu.Unlock()
		panic("observe: duplicate kind " + spec.Kind)
	}
	s.specs[spec.Kind] = spec
	s.mu.Unlock()
	s.queue.Register(spec.jobKind(), s.handler(spec.Kind), spec.MaxAttempts)
}

func (s *Service) spec(kind string) (Spec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp, ok := s.specs[kind]
	return sp, ok
}

// Kinds lists the registered kinds, sorted.
func (s *Service) Kinds() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.specs))
	for k := range s.specs {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

type refreshPayload struct {
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Generation int64  `json:"generation"`
	Revision   string `json:"revision"`
}

func idempotencyKey(kind, key string, gen int64) string {
	return fmt.Sprintf("observe:%s:%s:%d", kind, key, gen)
}

// Refresh admits one background refresh for key and returns at once with
// its job reference. A refresh already queued or running for the key is
// returned instead of a second one (Deduped). It never runs the check in
// the caller's request.
func (s *Service) Refresh(ctx context.Context, kind, key string) (Ref, error) {
	spec, ok := s.spec(kind)
	if !ok {
		return Ref{}, ErrUnknownKind
	}
	if !spec.enabled(ctx) {
		return Ref{}, ErrDisabled
	}
	if spec.Revision == nil {
		return Ref{}, errors.New("observe: spec has no revision function")
	}
	revision, err := spec.Revision(ctx, key)
	if err != nil {
		return Ref{}, err
	}
	if revision == "" {
		return Ref{}, notFoundErr{ErrNotFound}
	}
	ref := Ref{Kind: kind, Key: key}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var gen int64
		var jobID, status, pending string
		err := tx.QueryRowContext(ctx, `SELECT generation, job_id, attempt_status, pending_revision FROM observations WHERE kind = ? AND key = ?`, kind, key).Scan(&gen, &jobID, &status, &pending)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Deduplicate only against a refresh queued for the same revision:
		// after a configuration edit the in-flight job is superseded when
		// it runs, so a new generation is admitted for the new revision.
		if (status == "waiting" || status == "running") && jobID != "" && pending == revision {
			var jobStatus string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&jobStatus); err == nil && (jobStatus == "queued" || jobStatus == "running") {
				ref.JobID, ref.Generation, ref.Deduped = jobID, gen, true
				return nil
			}
		}
		gen++
		id, err := s.queue.EnqueueTx(ctx, tx, spec.jobKind(), refreshPayload{Kind: kind, Key: key, Generation: gen, Revision: revision}, jobs.Idempotency(idempotencyKey(kind, key, gen)))
		if err != nil {
			return err
		}
		now := db.FormatTime(s.now())
		_, err = tx.ExecContext(ctx, `INSERT INTO observations (kind, key, generation, job_id, pending_revision, attempt_status, attempt_started_at, attempt_finished_at,
			attempt_number, attempt_error, attempt_remediation, attempt_duration_ms, updated_at)
			VALUES (?, ?, ?, ?, ?, 'waiting', '', '', 0, '', '', NULL, ?)
			ON CONFLICT (kind, key) DO UPDATE SET generation = excluded.generation, job_id = excluded.job_id, pending_revision = excluded.pending_revision,
			attempt_status = 'waiting', attempt_started_at = '', attempt_finished_at = '', attempt_number = 0, attempt_error = '', attempt_remediation = '',
			attempt_duration_ms = NULL, updated_at = excluded.updated_at`, kind, key, gen, id, revision, now)
		if err != nil {
			return err
		}
		ref.JobID, ref.Generation = id, gen
		return nil
	})
	if err != nil {
		return Ref{}, fmt.Errorf("observe: refresh %s/%s: %w", kind, key, err)
	}
	return ref, nil
}

type row struct {
	kind, key          string
	generation         int64
	jobID              string
	pendingRevision    string
	attemptStatus      string
	attemptStartedAt   string
	attemptFinishedAt  string
	attemptNumber      int
	attemptError       string
	attemptRemediation string
	attemptDurationMS  sql.NullInt64
	successAt          string
	successRevision    string
	successDurationMS  sql.NullInt64
	successScope       string
	resultJSON         string
}

const rowColumns = `kind, key, generation, job_id, pending_revision, attempt_status, attempt_started_at, attempt_finished_at, attempt_number,
	attempt_error, attempt_remediation, attempt_duration_ms, success_at, success_revision, success_duration_ms, success_scope, result_json`

func scanRow(sc interface{ Scan(...any) error }) (row, error) {
	var r row
	err := sc.Scan(&r.kind, &r.key, &r.generation, &r.jobID, &r.pendingRevision, &r.attemptStatus, &r.attemptStartedAt, &r.attemptFinishedAt, &r.attemptNumber,
		&r.attemptError, &r.attemptRemediation, &r.attemptDurationMS, &r.successAt, &r.successRevision, &r.successDurationMS, &r.successScope, &r.resultJSON)
	return r, err
}

// Get returns the observation for key. A key that was never refreshed is
// returned with status "never" when it exists (Revision != ""), else ErrNotFound.
func (s *Service) Get(ctx context.Context, kind, key string) (Observation, error) {
	spec, ok := s.spec(kind)
	if !ok {
		return Observation{}, ErrUnknownKind
	}
	r, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+rowColumns+` FROM observations WHERE kind = ? AND key = ?`, kind, key))
	if errors.Is(err, sql.ErrNoRows) {
		if spec.Revision != nil {
			if rev, rerr := spec.Revision(ctx, key); rerr == nil && rev == "" {
				return Observation{}, notFoundErr{ErrNotFound}
			}
		}
		return Observation{Kind: kind, Key: key, Status: "never", Enabled: spec.enabled(ctx)}, nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("observe: get: %w", err)
	}
	return s.view(ctx, spec, r), nil
}

// List returns the saved observations of kind, keys sorted.
func (s *Service) List(ctx context.Context, kind string) ([]Observation, error) {
	spec, ok := s.spec(kind)
	if !ok {
		return nil, ErrUnknownKind
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+rowColumns+` FROM observations WHERE kind = ? ORDER BY key`, kind)
	if err != nil {
		return nil, fmt.Errorf("observe: list: %w", err)
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s.view(ctx, spec, r))
	}
	return out, rows.Err()
}

// All returns every saved observation of every registered kind.
func (s *Service) All(ctx context.Context) ([]Observation, error) {
	var out []Observation
	for _, kind := range s.Kinds() {
		list, err := s.List(ctx, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	if out == nil {
		out = []Observation{}
	}
	return out, nil
}

func (s *Service) view(ctx context.Context, spec Spec, r row) Observation {
	now := s.now()
	o := Observation{Kind: r.kind, Key: r.key, Enabled: spec.enabled(ctx), Status: "never"}
	if r.attemptStatus != "" {
		a := &Attempt{Status: r.attemptStatus, Number: r.attemptNumber, Error: r.attemptError, Remediation: r.attemptRemediation, JobID: r.jobID, Generation: r.generation}
		a.StartedAt, a.FinishedAt = optString(r.attemptStartedAt), optString(r.attemptFinishedAt)
		if r.attemptDurationMS.Valid {
			v := r.attemptDurationMS.Int64
			a.DurationMS = &v
		}
		o.Attempt = a
	}
	var current string
	var currentOK bool
	if spec.Revision != nil {
		if rev, err := spec.Revision(ctx, r.key); err == nil {
			current, currentOK = rev, true
		}
	}
	if r.successAt != "" {
		sv := &Saved{At: r.successAt, Revision: r.successRevision, Scope: r.successScope, Data: json.RawMessage(r.resultJSON)}
		if t, err := db.ParseTime(r.successAt); err == nil {
			sv.AgeSeconds = int64(now.Sub(t) / time.Second)
			if sv.AgeSeconds < 0 {
				sv.AgeSeconds = 0
			}
		}
		if r.successDurationMS.Valid {
			v := r.successDurationMS.Int64
			sv.DurationMS = &v
		}
		if currentOK {
			match := current == r.successRevision
			sv.RevisionMatch = &match
		}
		o.Saved = sv
	}
	switch {
	case !o.Enabled:
		o.Status = "disabled"
	case r.attemptStatus == "waiting" || r.attemptStatus == "running":
		o.Status = r.attemptStatus
	case r.attemptStatus == "failed":
		o.Status = "failed"
	case o.Saved != nil:
		o.Status = "fresh"
		if (o.Saved.RevisionMatch != nil && !*o.Saved.RevisionMatch) || (spec.Freshness > 0 && time.Duration(o.Saved.AgeSeconds)*time.Second > spec.Freshness) {
			o.Status = "stale"
		}
	case r.attemptStatus == "superseded":
		o.Status = "stale"
	}
	return o
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
