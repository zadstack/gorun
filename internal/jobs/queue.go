// Package jobs is the DB-backed job queue: enqueue, workers with retry and
// exponential backoff, and a coarse cron scheduler.
//
// Status lifecycle: queued → running → done, or on error back to queued with
// run_at = now + min(2^attempts, 300)s, and dead once attempts reaches
// max_attempts. A handler that returns an error after the worker's ctx was
// cancelled (shutdown) is requeued without counting an attempt. "failed" is a virtual status for Stats and List: queued jobs
// that already failed at least once (awaiting retry). attempts counts failed
// runs only.
package jobs

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the jobs table migrations ("migrations/NNNN_x.sql").
func Migrations() fs.FS { return migrations }

const (
	defaultMaxAttempts  = 5
	defaultPollInterval = time.Second
	defaultTickInterval = 30 * time.Second
	listLimitMax        = 500
)

// ErrNotFound is returned by Retry when no retryable job has the given id.
var ErrNotFound = errors.New("jobs: not found")

// Handler processes one job payload. A non-nil error schedules a retry.
type Handler func(ctx context.Context, payload json.RawMessage) error

type registration struct {
	handler     Handler
	maxAttempts int
}

type cronEntry struct {
	name    string
	every   time.Duration
	fn      func(ctx context.Context) error
	lastRun time.Time
}

// Queue is the job queue. Register handlers and crons before Start.
type Queue struct {
	db  *sql.DB
	log *slog.Logger

	mu       sync.RWMutex
	handlers map[string]registration
	crons    []*cronEntry

	now          func() time.Time
	pollInterval time.Duration
	tickInterval time.Duration
	wg           sync.WaitGroup
}

// Job is a row of the jobs table.
type Job struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	RunAt          string          `json:"run_at"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	LastError      string          `json:"last_error"`
	LockedBy       string          `json:"locked_by"`
	LockedAt       string          `json:"locked_at"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// Stats counts jobs by status. Queued excludes jobs awaiting retry, which
// are counted in Failed.
type Stats struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Dead    int `json:"dead"`
	Done    int `json:"done"`
}

// New returns a queue on d. The jobs migrations must already be applied.
func New(d *sql.DB, log *slog.Logger) *Queue {
	return &Queue{
		db:           d,
		log:          log,
		handlers:     map[string]registration{},
		now:          time.Now,
		pollInterval: defaultPollInterval,
		tickInterval: defaultTickInterval,
	}
}

// Register binds a handler to kind. maxAttempts <= 0 means the default (5).
func (q *Queue) Register(kind string, h Handler, maxAttempts int) {
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = registration{handler: h, maxAttempts: maxAttempts}
}

// Option configures Enqueue.
type Option func(*enqueueOptions)

type enqueueOptions struct {
	runAt          time.Time
	idempotencyKey string
}

// RunAt delays the job until t.
func RunAt(t time.Time) Option { return func(o *enqueueOptions) { o.runAt = t } }

// Idempotency dedupes on key: enqueueing an existing key returns the
// existing job's id instead of inserting a new job.
func Idempotency(key string) Option { return func(o *enqueueOptions) { o.idempotencyKey = key } }

type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Enqueue stores a job and returns its id. payload is marshalled to JSON.
//
// Enqueue writes on its own pool connection: never call it inside db.InTx
// (it would wait on the transaction's lock for busy_timeout and fail). Use
// EnqueueTx inside a transaction, or call Enqueue after commit.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, opts ...Option) (string, error) {
	return q.enqueue(ctx, q.db, kind, payload, opts...)
}

// EnqueueTx is Enqueue inside tx: the job exists only if tx commits.
func (q *Queue) EnqueueTx(ctx context.Context, tx *sql.Tx, kind string, payload any, opts ...Option) (string, error) {
	return q.enqueue(ctx, tx, kind, payload, opts...)
}

func (q *Queue) enqueue(ctx context.Context, ex dbtx, kind string, payload any, opts ...Option) (string, error) {
	o := enqueueOptions{runAt: q.now()}
	for _, opt := range opts {
		opt(&o)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jobs: marshal %s payload: %w", kind, err)
	}
	var key sql.NullString
	if o.idempotencyKey != "" {
		key = sql.NullString{String: o.idempotencyKey, Valid: true}
	}
	id, now := db.NewID(), db.FormatTime(q.now())
	res, err := ex.ExecContext(ctx, `INSERT INTO jobs
		(id, kind, payload_json, status, run_at, max_attempts, created_at, updated_at, idempotency_key)
		VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		id, kind, string(body), db.FormatTime(o.runAt), q.maxAttempts(kind), now, now, key)
	if err != nil {
		return "", fmt.Errorf("jobs: enqueue %s: %w", kind, err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return id, nil
	}
	var existing string
	if err := ex.QueryRowContext(ctx, `SELECT id FROM jobs WHERE idempotency_key = ?`, key).Scan(&existing); err != nil {
		return "", fmt.Errorf("jobs: lookup idempotency key: %w", err)
	}
	return existing, nil
}

func (q *Queue) maxAttempts(kind string) int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if r, ok := q.handlers[kind]; ok {
		return r.maxAttempts
	}
	return defaultMaxAttempts
}

// Stats returns job counts by status.
func (q *Queue) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	err := q.db.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE status = 'queued' AND attempts = 0),
		count(*) FILTER (WHERE status = 'running'),
		count(*) FILTER (WHERE status = 'queued' AND attempts > 0),
		count(*) FILTER (WHERE status = 'dead'),
		count(*) FILTER (WHERE status = 'done')
		FROM jobs`).Scan(&s.Queued, &s.Running, &s.Failed, &s.Dead, &s.Done)
	if err != nil {
		return Stats{}, fmt.Errorf("jobs: stats: %w", err)
	}
	return s, nil
}

// Retry requeues a queued, failed or dead job to run now with attempts reset.
func (q *Queue) Retry(ctx context.Context, id string) error {
	now := db.FormatTime(q.now())
	res, err := q.db.ExecContext(ctx, `UPDATE jobs
		SET status = 'queued', attempts = 0, run_at = ?, updated_at = ?, locked_by = NULL, locked_at = NULL
		WHERE id = ? AND status IN ('queued', 'failed', 'dead')`, now, now, id)
	if err != nil {
		return fmt.Errorf("jobs: retry %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns up to limit jobs, newest first. status "" means all;
// "failed" means queued jobs awaiting retry; "queued" includes those too.
func (q *Queue) List(ctx context.Context, status string, limit int) ([]Job, error) {
	if limit <= 0 || limit > listLimitMax {
		limit = listLimitMax
	}
	where, args := "1=1", []any{}
	switch status {
	case "":
	case "failed":
		where = "status = 'queued' AND attempts > 0"
	default:
		where, args = "status = ?", append(args, status)
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id, kind, payload_json, status, run_at, attempts, max_attempts,
		last_error, locked_by, locked_at, created_at, updated_at, idempotency_key
		FROM jobs WHERE `+where+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("jobs: list: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var payload string
		var lockedBy, lockedAt, key sql.NullString
		if err := rows.Scan(&j.ID, &j.Kind, &payload, &j.Status, &j.RunAt, &j.Attempts, &j.MaxAttempts,
			&j.LastError, &lockedBy, &lockedAt, &j.CreatedAt, &j.UpdatedAt, &key); err != nil {
			return nil, fmt.Errorf("jobs: scan: %w", err)
		}
		j.Payload = json.RawMessage(payload)
		j.LockedBy, j.LockedAt, j.IdempotencyKey = lockedBy.String, lockedAt.String, key.String
		out = append(out, j)
	}
	return out, rows.Err()
}
