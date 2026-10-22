// Package update implements the staged core update lifecycle: a persisted
// operation whose phases (compatibility, release verification, backup,
// activation, health and links, final acceptance) are reported and
// completed independently, protected by a durable lease, driven by a
// resumable job, and reconciled after a process restart. Sending a
// deployment request never counts as a successful update: activation needs
// the serving process to report the expected identity, and acceptance needs
// every required check to pass.
//
// Failure handling may activate a compatible prior artifact on the current
// data. It never restores a database or media snapshot over newer writes.
package update

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the update tables' migrations.
func Migrations() fs.FS { return migrations }

// ServiceName is the kernel service name the admin module publishes.
const ServiceName = "update"

// JobKind runs one operation; it is resumable and never retried blindly.
const JobKind = "update.run"

// Phase names, in lifecycle order; rollback phases are appended on demand.
const (
	PhaseCompatibility = "compatibility"
	PhaseVerification  = "verification"
	PhaseBackup        = "backup"
	PhaseActivation    = "activation"
	PhaseHealth        = "health"
	PhaseAcceptance    = "acceptance"

	PhaseRollbackCompatibility = "rollback_compatibility"
	PhaseRollbackActivation    = "rollback_activation"
	PhaseRollbackHealth        = "rollback_health"
)

var updatePlan = []string{PhaseCompatibility, PhaseVerification, PhaseBackup, PhaseActivation, PhaseHealth, PhaseAcceptance}
var rollbackPlan = []string{PhaseRollbackCompatibility, PhaseRollbackActivation, PhaseRollbackHealth}

// Operation statuses.
const (
	StatusPending        = "pending"
	StatusRunning        = "running"
	StatusSucceeded      = "succeeded"
	StatusFailed         = "failed"
	StatusInterrupted    = "interrupted"
	StatusRolledBack     = "rolled_back"
	StatusRollbackFailed = "rollback_failed"
)

// Phase statuses.
const (
	PhasePending     = "pending"
	PhaseRunning     = "running"
	PhasePassed      = "passed"
	PhaseFailed      = "failed"
	PhaseSkipped     = "skipped"
	PhaseInterrupted = "interrupted"
)

// Migration states recorded on an operation.
const (
	MigrationUnchecked = "unchecked"
	MigrationUnchanged = "unchanged"
	MigrationApplied   = "applied"
	MigrationUnknown   = "unknown"
)

// Errors returned by Request.
var (
	ErrInvalidTarget = errors.New("update: target is not a release version")
	ErrNotFound      = errors.New("update: operation not found")
)

// ErrConflict reports an operation or lease that blocks a new request.
type ErrConflict struct {
	OperationID string
	Holder      string
	Reason      string
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("update: conflict: %s (%s %s)", e.Reason, e.Holder, e.OperationID)
}

func (notFound) NotFound() bool { return true }

type notFound struct{ error }

func (n notFound) Unwrap() error { return n.error }

// Report is what an evidence provider returns for a phase.
type Report struct {
	OK          bool
	Reason      string
	Remediation string
	Retryable   bool
	Data        map[string]any
}

// Config wires the lifecycle to the host. Only RunningVersion and Source
// are required for requests; a nil Adapter means manual activation.
type Config struct {
	// RunningVersion is the serving process identity (the build string),
	// never a selected artifact or saved target.
	RunningVersion string
	Source         Source
	Adapter        Adapter
	// AllowDowngrade lets a lower target pass compatibility when the
	// operator accepts the recorded downgrade constraints.
	AllowDowngrade bool
	// RequireDigest fails verification when the source publishes no digest.
	RequireDigest bool
	// ActivationTimeout bounds waiting for a provider or operator to
	// activate the artifact (default 15 minutes). PollInterval is how often
	// the adapter status is read (default 10 seconds).
	ActivationTimeout time.Duration
	PollInterval      time.Duration
	// LeaseTTL and Heartbeat protect the operation across processes
	// (defaults 2 minutes and 30 seconds).
	LeaseTTL  time.Duration
	Heartbeat time.Duration
	// HealthLinks are absolute URLs that must answer 2xx after activation.
	HealthLinks []string

	// Evidence providers, supplied by the host (nil ones record
	// "unavailable" and fail the phase that needs them).
	Compatibility func(ctx context.Context, target string) (Report, error)
	Backup        func(ctx context.Context) (Report, error)
	Health        func(ctx context.Context) (Report, error)
	Link          func(ctx context.Context, url string) error
	// Schema lists the applied migrations ("module/name"); the difference
	// before and after activation decides whether the prior artifact is
	// still compatible with the current data.
	Schema func(ctx context.Context) ([]string, error)
}

// Phase is one reported lifecycle stage.
type Phase struct {
	Name        string          `json:"name"`
	Status      string          `json:"status" enum:"pending,running,passed,failed,skipped,interrupted"`
	StartedAt   *string         `json:"started_at"`
	FinishedAt  *string         `json:"finished_at"`
	DurationMS  *int64          `json:"duration_ms" doc:"Null until the phase finished"`
	Progress    *float64        `json:"progress" doc:"0..1 when measurable, else null"`
	Attempts    int             `json:"attempts"`
	Result      json.RawMessage `json:"result" doc:"Phase evidence; never credentials or hosts"`
	Error       string          `json:"error"`
	Remediation string          `json:"remediation"`
	Retryable   bool            `json:"retryable"`
}

// Operation is a persisted update with its phases.
type Operation struct {
	ID               string    `json:"id"`
	Status           string    `json:"status" enum:"pending,running,succeeded,failed,interrupted,rolled_back,rollback_failed"`
	CurrentPhase     string    `json:"current_phase"`
	RequestedVersion string    `json:"requested_version" doc:"Target asked for"`
	RunningVersion   string    `json:"running_version" doc:"Serving identity when the operation was requested"`
	ActivatedVersion *string   `json:"activated_version" doc:"Identity confirmed by the serving process after activation; null until then"`
	AcceptedVersion  *string   `json:"accepted_version" doc:"Target durably accepted after every required check passed; null otherwise"`
	ExpectedDigest   string    `json:"expected_digest"`
	Artifact         *Artifact `json:"artifact" doc:"Verified release identity; null before verification"`
	Previous         *Artifact `json:"previous" doc:"Prior artifact eligible for runtime rollback on current data; null when unknown"`
	BackupStamp      string    `json:"backup_stamp"`
	DeploymentRef    string    `json:"deployment_ref" doc:"Provider reference; accepted is not active"`
	RollbackRef      string    `json:"rollback_ref"`
	MigrationState   string    `json:"migration_state" enum:"unchecked,unchanged,applied,unknown" doc:"Separate from acceptance: whether activation applied migrations"`
	Source           string    `json:"source"`
	Adapter          string    `json:"adapter"`
	RequestedBy      string    `json:"requested_by"`
	Error            string    `json:"error"`
	Remediation      string    `json:"remediation"`
	Retryable        bool      `json:"retryable"`
	HeartbeatAt      *string   `json:"heartbeat_at"`
	CreatedAt        string    `json:"created_at"`
	StartedAt        *string   `json:"started_at"`
	FinishedAt       *string   `json:"finished_at"`
	UpdatedAt        string    `json:"updated_at"`
	Phases           []Phase   `json:"phases"`
}

// Terminal reports whether the operation can no longer change.
func (o Operation) Terminal() bool {
	switch o.Status {
	case StatusPending, StatusRunning:
		return false
	}
	return true
}

// Request asks for an update to Version.
type Request struct {
	Version        string `json:"version" doc:"Release version to activate, e.g. 0.15.0"`
	ExpectedDigest string `json:"expected_digest,omitempty" doc:"Optional sha256:… the verified artifact must match"`
	RequestedBy    string `json:"-"`
}

// Service runs operations.
type Service struct {
	db    *sql.DB
	queue *jobs.Queue
	lease *Lease
	log   *slog.Logger
	cfg   Config
	now   func() time.Time
}

// New returns a service. Call Register before the queue starts.
func New(d *sql.DB, q *jobs.Queue, log *slog.Logger, cfg Config) *Service {
	if cfg.ActivationTimeout <= 0 {
		cfg.ActivationTimeout = 15 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 30 * time.Second
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 2 * time.Minute
	}
	if cfg.Adapter == nil {
		cfg.Adapter = ManualAdapter{}
	}
	s := &Service{db: d, queue: q, log: log, cfg: cfg, now: time.Now}
	s.lease = NewLease(d, cfg.LeaseTTL)
	return s
}

// Lease exposes the durable lease so backup and restore share it.
func (s *Service) Lease() *Lease { return s.lease }

// Configured reports whether a trusted release source exists.
func (s *Service) Configured() bool { return s.cfg.Source != nil }

// SourceName and AdapterName describe the configured integrations.
func (s *Service) SourceName() string {
	if s.cfg.Source == nil {
		return ""
	}
	return s.cfg.Source.Name()
}

// AdapterName returns the configured deployment adapter name.
func (s *Service) AdapterName() string { return s.cfg.Adapter.Name() }

// RunningVersion is the serving identity.
func (s *Service) RunningVersion() string { return s.cfg.RunningVersion }

// Register binds the job handler.
func (s *Service) Register() {
	s.queue.Register(JobKind, s.run, 1)
}

type runPayload struct {
	OperationID string `json:"operation_id"`
	Token       string `json:"token"`
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Request validates, records and queues an operation, returning at once.
// A pending or running operation, a held lease, or a prior deployment the
// provider still reports in progress is a conflict.
func (s *Service) Request(ctx context.Context, req Request) (Operation, error) {
	if s.cfg.Source == nil {
		return Operation{}, ErrNotConfigured
	}
	target := normalizeVersion(req.Version)
	if _, ok := parseVersion(target); !ok {
		return Operation{}, ErrInvalidTarget
	}
	req.ExpectedDigest = strings.ToLower(strings.TrimSpace(req.ExpectedDigest))
	if req.ExpectedDigest != "" && !digestPattern.MatchString(req.ExpectedDigest) {
		return Operation{}, ErrInvalidTarget
	}
	if err := s.reconcileAccepted(ctx); err != nil {
		return Operation{}, err
	}
	id, token := db.NewID(), newToken()
	now := db.FormatTime(s.now())
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var active string
		err := tx.QueryRowContext(ctx, `SELECT id FROM update_operations WHERE status IN ('pending', 'running') ORDER BY created_at DESC LIMIT 1`).Scan(&active)
		if err == nil {
			return &ErrConflict{OperationID: active, Holder: HolderUpdate, Reason: "an update operation is already in progress"}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := s.lease.acquireTx(ctx, tx, HolderUpdate, token, id); err != nil {
			var held *ErrLeaseHeld
			if errors.As(err, &held) {
				return &ErrConflict{OperationID: held.OperationID, Holder: held.Holder, Reason: "the data directory is busy with " + held.Holder}
			}
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO update_operations (id, status, requested_version, expected_digest, requested_by, running_version,
			source, adapter, run_token, heartbeat_at, created_at, updated_at) VALUES (?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, target, req.ExpectedDigest, req.RequestedBy, s.cfg.RunningVersion, s.cfg.Source.Name(), s.cfg.Adapter.Name(), token, now, now, now)
		if err != nil {
			return err
		}
		for i, name := range updatePlan {
			if _, err := tx.ExecContext(ctx, `INSERT INTO update_phases (operation_id, name, seq, updated_at) VALUES (?, ?, ?, ?)`, id, name, i, now); err != nil {
				return err
			}
		}
		_, err = s.queue.EnqueueTx(ctx, tx, JobKind, runPayload{OperationID: id, Token: token}, jobs.Idempotency("update:"+id+":start"))
		return err
	})
	if err != nil {
		return Operation{}, err
	}
	return s.Get(ctx, id)
}

// reconcileAccepted refuses a new request while the provider still reports
// the most recent operation's deployment as in progress.
func (s *Service) reconcileAccepted(ctx context.Context) error {
	var id, ref, status string
	err := s.db.QueryRowContext(ctx, `SELECT id, deployment_ref, status FROM update_operations WHERE deployment_ref <> '' ORDER BY created_at DESC LIMIT 1`).Scan(&id, &ref, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != StatusInterrupted && status != StatusFailed {
		return nil
	}
	act, err := s.cfg.Adapter.Status(ctx, ref)
	if err != nil {
		return nil
	}
	if act.Status == ActivationQueued || act.Status == ActivationRunning {
		return &ErrConflict{OperationID: id, Holder: HolderUpdate, Reason: "a previously accepted deployment is still in progress at the provider"}
	}
	return nil
}

const opColumns = `id, status, current_phase, requested_version, expected_digest, requested_by, running_version, activated_version, accepted_version,
	artifact_json, previous_json, backup_stamp, deployment_ref, rollback_ref, migration_state, source, adapter, heartbeat_at, error, remediation, retryable,
	created_at, started_at, finished_at, updated_at`

func scanOp(sc interface{ Scan(...any) error }) (Operation, error) {
	var o Operation
	var activated, accepted, artifact, previous, heartbeat, started, finished string
	var retryable int
	err := sc.Scan(&o.ID, &o.Status, &o.CurrentPhase, &o.RequestedVersion, &o.ExpectedDigest, &o.RequestedBy, &o.RunningVersion, &activated, &accepted,
		&artifact, &previous, &o.BackupStamp, &o.DeploymentRef, &o.RollbackRef, &o.MigrationState, &o.Source, &o.Adapter, &heartbeat, &o.Error, &o.Remediation, &retryable,
		&o.CreatedAt, &started, &finished, &o.UpdatedAt)
	if err != nil {
		return o, err
	}
	o.ActivatedVersion, o.AcceptedVersion = opt(activated), opt(accepted)
	o.HeartbeatAt, o.StartedAt, o.FinishedAt = opt(heartbeat), opt(started), opt(finished)
	o.Retryable = retryable == 1
	if artifact != "null" {
		var a Artifact
		if json.Unmarshal([]byte(artifact), &a) == nil {
			o.Artifact = &a
		}
	}
	if previous != "null" {
		var a Artifact
		if json.Unmarshal([]byte(previous), &a) == nil {
			o.Previous = &a
		}
	}
	return o, nil
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Get returns one operation with its phases.
func (s *Service) Get(ctx context.Context, id string) (Operation, error) {
	o, err := scanOp(s.db.QueryRowContext(ctx, `SELECT `+opColumns+` FROM update_operations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, notFound{ErrNotFound}
	}
	if err != nil {
		return Operation{}, fmt.Errorf("update: get: %w", err)
	}
	if o.Phases, err = s.phases(ctx, id); err != nil {
		return Operation{}, err
	}
	return o, nil
}

func (s *Service) phases(ctx context.Context, id string) ([]Phase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, status, started_at, finished_at, duration_ms, progress, attempts, result_json, error, remediation, retryable
		FROM update_phases WHERE operation_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("update: phases: %w", err)
	}
	defer rows.Close()
	out := []Phase{}
	for rows.Next() {
		var p Phase
		var started, finished, result string
		var duration sql.NullInt64
		var progress sql.NullFloat64
		var retryable int
		if err := rows.Scan(&p.Name, &p.Status, &started, &finished, &duration, &progress, &p.Attempts, &result, &p.Error, &p.Remediation, &retryable); err != nil {
			return nil, err
		}
		p.StartedAt, p.FinishedAt = opt(started), opt(finished)
		if duration.Valid {
			v := duration.Int64
			p.DurationMS = &v
		}
		if progress.Valid {
			v := progress.Float64
			p.Progress = &v
		}
		p.Result, p.Retryable = json.RawMessage(result), retryable == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// List returns the newest operations first, without phases.
func (s *Service) List(ctx context.Context, limit int) ([]Operation, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+opColumns+` FROM update_operations ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("update: list: %w", err)
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		o.Phases = []Phase{}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Active returns the pending or running operation, if any.
func (s *Service) Active(ctx context.Context) (*Operation, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM update_operations WHERE status IN ('pending', 'running') ORDER BY created_at DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// Last returns the most recent operation, if any.
func (s *Service) Last(ctx context.Context) (*Operation, error) {
	list, err := s.List(ctx, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	o, err := s.Get(ctx, list[0].ID)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// Accepted returns the last durably accepted target version, if any.
func (s *Service) Accepted(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT accepted_version FROM update_operations WHERE status = 'succeeded' ORDER BY finished_at DESC, id DESC LIMIT 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Source returns the configured release source (nil when unconfigured).
func (s *Service) Source() Source { return s.cfg.Source }

// NewOwnerToken returns a random lease owner token.
func NewOwnerToken() string { return newToken() }
