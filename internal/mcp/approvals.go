package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// Approval statuses (mcp_approvals.status) and the agent-facing pending
// status. "approved" with an empty result means the call is executing;
// "failed" is an approved call whose execution errored or was interrupted.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
	StatusFailed   = "failed"

	statusPendingApproval = "pending_approval"

	maxParkedArgs     = 256 << 10
	interruptedAfter  = 10 * time.Minute
	effectiveStatusSQ = `CASE WHEN status = 'pending' AND expires_at <= ? THEN 'expired' ELSE status END`
)

var (
	// ErrApprovalNotFound is returned for unknown approvals (and for
	// approvals of another API key in check_approval).
	ErrApprovalNotFound = errors.New("mcp: approval not found")
	// ErrDecisionForbidden is returned when the decider may not decide: the
	// requesting key itself, or an API key without an explicit mcp.approve
	// scope ("*" does not count).
	ErrDecisionForbidden = errors.New("mcp: this principal may not decide approvals")
)

// Approval is one mcp_approvals row. Status is the effective status: a
// pending row past expires_at reads as expired even before the cron runs.
type Approval struct {
	ID          string          `json:"id"`
	APIKeyID    string          `json:"api_key_id"`
	KeyName     string          `json:"key_name"`
	RequestedBy string          `json:"requested_by"`
	Requester   string          `json:"requester"`
	Tool        string          `json:"tool"`
	Args        json.RawMessage `json:"args"`
	Status      string          `json:"status"`
	RequestedAt string          `json:"requested_at"`
	ExpiresAt   string          `json:"expires_at"`
	DecidedAt   string          `json:"decided_at"`
	DecidedBy   string          `json:"decided_by"`
	ExecutedAt  string          `json:"executed_at"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type approvalStore struct {
	s *server
}

const approvalColumns = `id, api_key_id, key_name, requested_by, requester, tool, args_json, status, requested_at, expires_at,
	decided_at, decided_by, executed_at, result_json`

func scanApproval(row interface{ Scan(...any) error }) (*Approval, error) {
	var a Approval
	var args, result string
	err := row.Scan(&a.ID, &a.APIKeyID, &a.KeyName, &a.RequestedBy, &a.Requester, &a.Tool, &args, &a.Status,
		&a.RequestedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecidedBy, &a.ExecutedAt, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrApprovalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: scan approval: %w", err)
	}
	a.Args = json.RawMessage(args)
	if result != "" {
		a.Result = json.RawMessage(result)
	}
	if a.Status == StatusPending && a.ExpiresAt <= db.Now() {
		a.Status = StatusExpired
	}
	return &a, nil
}

// validateParked checks a sensitive call's arguments before parking them:
// at most 256 KB and strictly decodable into the tool's input type, so a
// human never approves arguments that cannot run.
func validateParked(t kernel.MCPTool, args json.RawMessage) error {
	if len(args) > maxParkedArgs {
		return invalid("arguments exceed 256 KB", map[string]string{"arguments": "too_large"})
	}
	var target any = &map[string]any{}
	if t.Input != nil {
		typ := reflect.TypeOf(t.Input)
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		target = reflect.New(typ).Interface()
	}
	return decode(args, target)
}

// create parks a sensitive call made by the API key in ctx.
func (st *approvalStore) create(ctx context.Context, tool string, args json.RawMessage) (*Approval, error) {
	actor := auth.ActorFromContext(ctx)
	if actor.Kind != "api_key" {
		return nil, errForbidden()
	}
	now := time.Now()
	a := &Approval{ID: db.NewID(), APIKeyID: actor.ID, KeyName: actor.Name, Tool: tool, Args: args, Status: StatusPending,
		RequestedAt: db.FormatTime(now), ExpiresAt: db.FormatTime(now.Add(approvalTTL))}
	if u := auth.UserFromContext(ctx); u != nil {
		a.RequestedBy, a.Requester = u.ID, u.Name
		if a.Requester == "" {
			a.Requester = u.Email
		}
	}
	_, err := st.s.d.DB.ExecContext(ctx, `INSERT INTO mcp_approvals (id, api_key_id, key_name, requested_by, requester, tool, args_json,
		status, requested_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.APIKeyID, a.KeyName, a.RequestedBy, a.Requester, a.Tool, string(args), a.Status, a.RequestedAt, a.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("mcp: create approval: %w", err)
	}
	return a, nil
}

func (st *approvalStore) get(ctx context.Context, id string) (*Approval, error) {
	return scanApproval(st.s.d.DB.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM mcp_approvals WHERE id = ?`, id))
}

// list returns approvals whose effective status is status ("" = all),
// newest first.
func (st *approvalStore) list(ctx context.Context, status string, limit, offset int) ([]Approval, error) {
	rows, err := st.s.d.DB.QueryContext(ctx, `SELECT `+approvalColumns+` FROM mcp_approvals
		WHERE ? = '' OR `+effectiveStatusSQ+` = ? ORDER BY requested_at DESC, id DESC LIMIT ? OFFSET ?`,
		status, db.Now(), status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("mcp: list approvals: %w", err)
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// count counts approvals whose effective status is status ("" = all).
func (st *approvalStore) count(ctx context.Context, status string) (int, error) {
	var n int
	err := st.s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM mcp_approvals WHERE ? = '' OR `+effectiveStatusSQ+` = ?`,
		status, db.Now(), status).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("mcp: count approvals: %w", err)
	}
	return n, nil
}

// claim moves a live pending approval to status. Only the caller that gets
// true may act on it (an approval executes once).
func (st *approvalStore) claim(ctx context.Context, id, status, deciderID string) (bool, error) {
	now := db.Now()
	res, err := st.s.d.DB.ExecContext(ctx, `UPDATE mcp_approvals SET status = ?, decided_at = ?, decided_by = ?
		WHERE id = ? AND status = 'pending' AND expires_at > ?`, status, now, deciderID, id, now)
	if err != nil {
		return false, fmt.Errorf("mcp: decide approval: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Approve executes the pending call once as the requesting key and stores
// the result (status approved, or failed when execution errors). won is
// false when the approval was already decided or expired: nothing ran.
func (st *approvalStore) Approve(ctx context.Context, id string) (a *Approval, won bool, err error) {
	if err := st.checkDecider(ctx, id); err != nil {
		return nil, false, err
	}
	if won, err = st.claim(ctx, id, StatusApproved, deciderID(ctx)); err != nil {
		return nil, false, err
	}
	if a, err = st.get(ctx, id); err != nil || !won {
		return a, won, err
	}
	st.s.d.Audit.Record(ctx, "mcp.approval.approve", "mcp_approval", id, map[string]any{"tool": a.Tool, "api_key_id": a.APIKeyID})
	data, runErr := st.s.runApproved(ctx, a)
	status := StatusApproved
	if runErr != nil {
		status = StatusFailed
	}
	result, mErr := json.Marshal(st.s.envelope(a.Tool, data, runErr))
	if mErr != nil {
		status = StatusFailed
		result, _ = json.Marshal(newEnvelope(nil, &ToolError{Code: codeInternal, Message: "result is not JSON-serialisable"}))
	}
	if _, err := st.s.d.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE mcp_approvals SET status = ?, result_json = ?, executed_at = ? WHERE id = ?`,
		status, string(result), db.Now(), id); err != nil {
		return nil, true, fmt.Errorf("mcp: store approval result: %w", err)
	}
	a, err = st.get(ctx, id)
	return a, true, err
}

// Deny rejects a pending approval; won is false for decided or expired
// ones (no-op).
func (st *approvalStore) Deny(ctx context.Context, id string) (*Approval, bool, error) {
	if err := st.checkDecider(ctx, id); err != nil {
		return nil, false, err
	}
	won, err := st.claim(ctx, id, StatusDenied, deciderID(ctx))
	if err != nil {
		return nil, false, err
	}
	a, err := st.get(ctx, id)
	if err == nil && won {
		st.s.d.Audit.Record(ctx, "mcp.approval.deny", "mcp_approval", id, map[string]any{"tool": a.Tool, "api_key_id": a.APIKeyID})
	}
	return a, won, err
}

// checkDecider allows decisions from user sessions (the route or API op
// already required mcp.approve) and from API keys that list mcp.approve
// explicitly ("*" does not count) and did not request the call themselves.
func (st *approvalStore) checkDecider(ctx context.Context, id string) error {
	a, err := st.get(ctx, id)
	if err != nil {
		return err
	}
	actor := auth.ActorFromContext(ctx)
	if actor.Kind != "api_key" {
		return nil
	}
	if actor.ID == a.APIKeyID {
		return ErrDecisionForbidden
	}
	k, err := st.s.findKey(ctx, actor.ID)
	if err != nil || !slices.Contains(k.Scopes, PermApprove) {
		return ErrDecisionForbidden
	}
	return nil
}

func deciderID(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// expire (cron) marks pending approvals past expires_at as expired, and
// approved rows whose execution never stored a result (process crash) as
// failed.
func (st *approvalStore) expire(ctx context.Context) error {
	now := db.Now()
	res, err := st.s.d.DB.ExecContext(ctx, `UPDATE mcp_approvals SET status = 'expired', decided_at = ? WHERE status = 'pending' AND expires_at <= ?`,
		now, now)
	if err != nil {
		return fmt.Errorf("mcp: expire approvals: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		st.s.d.Audit.Record(ctx, "mcp.approval.expire", "mcp_approval", "", map[string]any{"count": n})
	}
	interrupted, _ := json.Marshal(newEnvelope(nil, &ToolError{Code: "interrupted",
		Message: "execution was interrupted before a result was stored; check whether it took effect"}))
	if _, err := st.s.d.DB.ExecContext(ctx, `UPDATE mcp_approvals SET status = 'failed', result_json = ?, executed_at = ?
		WHERE status = 'approved' AND result_json = '' AND decided_at <= ?`,
		string(interrupted), now, db.FormatTime(time.Now().Add(-interruptedAfter))); err != nil {
		return fmt.Errorf("mcp: fail interrupted approvals: %w", err)
	}
	return nil
}

// pendingCounts returns pending approvals and how many are past their
// expiry (the cron has not caught up).
func (st *approvalStore) pendingCounts(ctx context.Context) (pending, stale int, err error) {
	err = st.s.d.DB.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(expires_at <= ?), 0) FROM mcp_approvals WHERE status = 'pending'`,
		db.Now()).Scan(&pending, &stale)
	return pending, stale, err
}

// findKey returns the API key id (revoked or not).
func (s *server) findKey(ctx context.Context, id string) (*auth.APIKey, error) {
	keys, err := s.keys.List(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(keys, func(k auth.APIKey) bool { return k.ID == id })
	if i < 0 {
		return nil, auth.ErrNotFound
	}
	return &keys[i], nil
}

// runApproved executes an approved call as the requesting API key: the key
// must still be active, its owner active, and its scopes (and the owner's
// role) must still grant the tool. The call runs as the key (actor kind
// api_key) and is audited as mcp.call with the approval and key ids.
func (s *server) runApproved(ctx context.Context, a *Approval) (any, error) {
	t, ok := s.tool(a.Tool)
	if !ok {
		return nil, notFound("tool " + a.Tool)
	}
	actx, err := s.keyActor(ctx, a.APIKeyID, t)
	start := time.Now()
	var data any
	if err == nil {
		data, err = s.execute(actx, t, a.Args)
	} else {
		actx = ctx
	}
	meta := map[string]any{"args_hash": argsHash(a.Args), "ok": err == nil, "duration_ms": time.Since(start).Milliseconds(),
		"approval_id": a.ID, "api_key_id": a.APIKeyID, "approved_by": deciderID(ctx), "executed": true}
	if err != nil {
		meta["error_code"] = s.classify(t.Name, err).Code
	}
	s.d.Audit.Record(actx, "mcp.call", "tool", t.Name, meta)
	return data, err
}

// keyActor rebuilds the acting context of the key keyID (see runApproved)
// with auth.WithAPIKeyActor, so the call runs and is audited AS THE KEY:
// the key must still be valid and its scopes and owner's role must still
// grant every scope of the tool.
func (s *server) keyActor(ctx context.Context, keyID string, t kernel.MCPTool) (context.Context, error) {
	actx, err := auth.WithAPIKeyActor(context.WithoutCancel(ctx), s.d, keyID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return nil, &ToolError{Code: codeUnauthorized, Message: "the API key no longer exists"}
	case errors.Is(err, auth.ErrKeyRevoked):
		return nil, &ToolError{Code: codeUnauthorized, Message: "the API key was revoked"}
	case errors.Is(err, auth.ErrKeyExpired):
		return nil, &ToolError{Code: codeUnauthorized, Message: "the API key expired"}
	case errors.Is(err, auth.ErrKeyOwnerInactive):
		return nil, &ToolError{Code: codeUnauthorized, Message: "the API key's owner is not active"}
	case err != nil:
		return nil, err
	}
	for _, sc := range t.Scopes {
		if !auth.Can(actx, sc) { // key scope AND owner role
			return nil, errForbidden()
		}
	}
	return actx, nil
}

// checkApprovalInput is the input of check_approval.
type checkApprovalInput struct {
	ApprovalID string `json:"approval_id" jsonschema:"the approval_id returned by a sensitive tool"`
}

// checkApprovalResult is the output of check_approval.
type checkApprovalResult struct {
	ApprovalID string          `json:"approval_id"`
	Tool       string          `json:"tool"`
	Status     string          `json:"status"`
	ExpiresAt  string          `json:"expires_at,omitempty"`
	DecidedAt  string          `json:"decided_at,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// checkApproval reports an approval requested by the calling key.
func (s *server) checkApproval(ctx context.Context, in checkApprovalInput) (any, error) {
	a, err := s.approvals.get(ctx, in.ApprovalID)
	if errors.Is(err, ErrApprovalNotFound) || (err == nil && a.APIKeyID != auth.ActorFromContext(ctx).ID) {
		return nil, notFound("approval")
	}
	if err != nil {
		return nil, err
	}
	out := checkApprovalResult{ApprovalID: a.ID, Tool: a.Tool, Status: a.Status, DecidedAt: a.DecidedAt}
	switch a.Status {
	case StatusPending:
		out.Status, out.ExpiresAt = statusPendingApproval, a.ExpiresAt
	case StatusApproved, StatusFailed:
		out.Result = a.Result
	}
	return out, nil
}
