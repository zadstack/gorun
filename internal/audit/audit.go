// Package audit writes and reads the audit log.
package audit

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the audit_log migrations ("migrations/NNNN_x.sql").
func Migrations() fs.FS { return migrations }

const (
	defaultListLimit = 50
	maxListLimit     = 500
)

// Actor is who performed an action. Kinds: user, api_key, system, extension.
type Actor struct{ Kind, ID, Name string }

// SystemActor is recorded when no actor is found in the context.
var SystemActor = Actor{Kind: "system", ID: "", Name: "system"}

// ActorFromContext resolves the acting principal. The auth package (later
// in the boot order, so audit cannot import it) replaces this at init.
var ActorFromContext = func(ctx context.Context) (Actor, bool) { return Actor{}, false }

type ipKey struct{}

// WithIP stores the client IP for Record (set by HTTP middleware).
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey{}, ip)
}

// IPFromContext returns the client IP stored by WithIP, or "".
func IPFromContext(ctx context.Context) string { return ipFrom(ctx) }

func ipFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ipKey{}).(string)
	return ip
}

// Entry is one audit_log row.
type Entry struct {
	ID         string          `json:"id"`
	At         string          `json:"at"`
	Actor      Actor           `json:"actor"`
	Action     string          `json:"action"`
	TargetKind string          `json:"target_kind"`
	TargetID   string          `json:"target_id"`
	IP         string          `json:"ip"`
	Meta       json.RawMessage `json:"meta"`
}

// ListQuery filters List. Empty fields match everything. Since is inclusive
// and Until exclusive (RFC3339). Limit defaults to 50, max 500.
type ListQuery struct {
	Action, ActorKind, ActorID, TargetKind, TargetID string
	Since, Until                                     string
	Limit, Offset                                    int
}

// Writer records and lists audit entries.
type Writer struct {
	db  *sql.DB
	log *slog.Logger
}

// New returns a writer on d. The audit migrations must already be applied.
func New(d *sql.DB, log *slog.Logger) *Writer { return &Writer{db: d, log: log} }

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Record appends an entry. The actor comes from ActorFromContext (system
// when absent) and the IP from WithIP. The write ignores ctx cancellation so
// an entry is not lost when the request ends. Failures are logged, never
// returned, so auditing cannot break the audited operation.
//
// Record writes on its own pool connection: never call it inside db.InTx
// (it would wait on the transaction's lock for busy_timeout and fail). Use
// RecordTx inside a transaction, or call Record after commit.
func (w *Writer) Record(ctx context.Context, action, targetKind, targetID string, meta any) {
	w.record(ctx, context.WithoutCancel(ctx), w.db, action, targetKind, targetID, meta)
}

// RecordTx is Record inside tx: the entry commits or rolls back with it.
func (w *Writer) RecordTx(ctx context.Context, tx *sql.Tx, action, targetKind, targetID string, meta any) {
	w.record(ctx, ctx, tx, action, targetKind, targetID, meta)
}

// record resolves actor and IP from ctx and writes through ex using execCtx.
func (w *Writer) record(ctx, execCtx context.Context, ex execer, action, targetKind, targetID string, meta any) {
	actor, ok := ActorFromContext(ctx)
	if !ok {
		actor = SystemActor
	}
	metaJSON := "{}"
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			w.log.Error("audit: marshal meta", "action", action, "err", err)
		} else {
			metaJSON = string(b)
		}
	}
	_, err := ex.ExecContext(execCtx, `INSERT INTO audit_log
		(id, at, actor_kind, actor_id, actor_name, action, target_kind, target_id, ip, meta_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		db.NewID(), db.Now(), actor.Kind, actor.ID, actor.Name, action, targetKind, targetID, ipFrom(ctx), metaJSON)
	if err != nil {
		w.log.Error("audit: record", "action", action, "target", targetKind+":"+targetID, "err", err)
	}
}

// List returns matching entries newest first and the total match count.
func (w *Writer) List(ctx context.Context, q ListQuery) ([]Entry, int, error) {
	var conds []string
	var args []any
	add := func(cond, v string) {
		if v != "" {
			conds = append(conds, cond)
			args = append(args, v)
		}
	}
	add("action = ?", q.Action)
	add("actor_kind = ?", q.ActorKind)
	add("actor_id = ?", q.ActorID)
	add("target_kind = ?", q.TargetKind)
	add("target_id = ?", q.TargetID)
	add("at >= ?", q.Since)
	add("at < ?", q.Until)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := w.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit: count: %w", err)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)
	rows, err := w.db.QueryContext(ctx, `SELECT id, at, actor_kind, actor_id, actor_name, action,
		target_kind, target_id, ip, meta_json FROM audit_log`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var meta string
		if err := rows.Scan(&e.ID, &e.At, &e.Actor.Kind, &e.Actor.ID, &e.Actor.Name, &e.Action,
			&e.TargetKind, &e.TargetID, &e.IP, &meta); err != nil {
			return nil, 0, fmt.Errorf("audit: scan: %w", err)
		}
		e.Meta = json.RawMessage(meta)
		out = append(out, e)
	}
	return out, total, rows.Err()
}
