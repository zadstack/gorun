package workagents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// LinearPlan deliberately does not accept imported graphs or conditions. An
// operator must map them explicitly without silently losing source semantics.
type LinearPlan struct {
	Name        string       `json:"name"`
	Input       string       `json:"input"`
	RequestKey  string       `json:"request_key"`
	Steps       []LinearStep `json:"steps"`
	DraftTitle  string       `json:"draft_title,omitempty"`
	DraftLocale string       `json:"draft_locale,omitempty"`
}
type LinearStep struct {
	Kind    string `json:"kind"` // agent, approval, draft
	AgentID string `json:"agent_id,omitempty"`
}
type LinearFlow struct {
	ID           string     `json:"id"`
	OwnerID      string     `json:"owner_id"`
	Plan         LinearPlan `json:"plan"`
	Step         int        `json:"step"`
	Status       string     `json:"status"`
	Result       string     `json:"result"`
	ResultHash   string     `json:"result_hash"`
	ErrorCode    string     `json:"error_code"`
	ApprovedStep int        `json:"approved_step"`
}

func flowHash(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:])
}

func (s *service) linearFlow(ctx context.Context, id string) (LinearFlow, error) {
	var f LinearFlow
	var plan string
	err := s.d.DB.QueryRowContext(ctx, `SELECT id,owner_id,plan,step,status,result,error_code,approved_step FROM workagents_linear_flows WHERE id=? AND (owner_id=? OR ?=1)`, id, owner(ctx), manager(ctx)).Scan(&f.ID, &f.OwnerID, &plan, &f.Step, &f.Status, &f.Result, &f.ErrorCode, &f.ApprovedStep)
	if errors.Is(err, sql.ErrNoRows) {
		return f, errNotFound
	}
	if err != nil {
		return f, err
	}
	if json.Unmarshal([]byte(plan), &f.Plan) != nil {
		return f, errInvalid
	}
	f.ResultHash = flowHash(f.Result)
	return f, nil
}
func (s *service) validateLinearPlan(ctx context.Context, p LinearPlan) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 160 || strings.TrimSpace(p.Input) == "" || len(p.Input) > maxInputBytes || p.RequestKey == "" || len(p.RequestKey) > 128 || len(p.Steps) < 2 || len(p.Steps) > 12 {
		return errInvalid
	}
	approval := false
	for i, step := range p.Steps {
		switch step.Kind {
		case "approval":
			if i == 0 || step.AgentID != "" || p.Steps[i-1].Kind != "agent" {
				return errInvalid
			}
			approval = true
		case "agent", "draft":
			a, err := s.agent(ctx, step.AgentID)
			if err != nil {
				return err
			}
			// One owner throughout: manager access must not let stages cross accounts.
			if a.OwnerID != owner(ctx) || !a.Enabled || len(a.UnsupportedTools) > 0 {
				return errUnavailable
			}
			if step.Kind == "draft" {
				if a.ExecutionKind != "content_draft" || i != len(p.Steps)-1 || i == 0 || p.Steps[i-1].Kind != "approval" || !auth.Can(ctx, "content.create") || strings.TrimSpace(p.DraftTitle) == "" || len(p.DraftTitle) > 240 || (p.DraftLocale != "ar" && p.DraftLocale != "en") {
					return errInvalid
				}
			} else if a.ExecutionKind != "text" && !(i == 0 && (a.ExecutionKind == "github_stars" || a.ExecutionKind == "github_starred")) {
				return errUnavailable
			}
		default:
			return errInvalid
		}
	}
	if !approval {
		return errInvalid
	}
	return nil
}
func (s *service) startLinearFlow(ctx context.Context, p LinearPlan) (LinearFlow, error) {
	if !manager(ctx) || owner(ctx) == "" {
		return LinearFlow{}, errUnavailable
	}
	if err := s.validateLinearPlan(ctx, p); err != nil {
		return LinearFlow{}, err
	}
	encoded, _ := json.Marshal(p)
	id := db.NewID()
	now := db.FormatTime(s.now())
	_, err := s.d.DB.ExecContext(ctx, `INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,result,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(owner_id,request_key) DO NOTHING`, id, owner(ctx), p.RequestKey, string(encoded), p.Input, now, now)
	if err != nil {
		return LinearFlow{}, err
	}
	var stored string
	if err = s.d.DB.QueryRowContext(ctx, `SELECT id,plan FROM workagents_linear_flows WHERE owner_id=? AND request_key=?`, owner(ctx), p.RequestKey).Scan(&id, &stored); err != nil {
		return LinearFlow{}, err
	}
	if stored != string(encoded) {
		return LinearFlow{}, errConflict
	}
	if s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.linear.started", "workagent_linear_flow", id, nil)
	}
	return s.linearFlow(ctx, id)
}

type LinearDecision struct {
	Approve    bool   `json:"approve"`
	Step       int    `json:"step"`
	ResultHash string `json:"result_hash"`
}

func (s *service) decideLinearFlow(ctx context.Context, id string, d LinearDecision) (LinearFlow, error) {
	if !manager(ctx) {
		return LinearFlow{}, errUnavailable
	}
	f, err := s.linearFlow(ctx, id)
	if err != nil {
		return f, err
	}
	// Approval is fresh, exact-step, exact-output and owner-bound, never inherited
	// from a source archive or a manager acting as an unrelated account.
	if f.OwnerID != owner(ctx) {
		return f, errUnavailable
	}
	if f.Status != "awaiting_approval" || f.Step != d.Step || f.ResultHash != d.ResultHash {
		return f, errConflict
	}
	status := "rejected"
	next := f.Step
	if d.Approve {
		status = "running"
		next++
	}
	result, err := s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET status=?,step=?,approved_step=?,approved_by=?,approved_hash=?,updated_at=? WHERE id=? AND status='awaiting_approval' AND step=? AND result=?`, status, next, f.Step, owner(ctx), d.ResultHash, db.FormatTime(s.now()), id, f.Step, f.Result)
	if err != nil {
		return f, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return f, errConflict
	}
	if s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.linear.decided", "workagent_linear_flow", id, map[string]any{"approved": d.Approve, "step": f.Step, "result_hash": d.ResultHash})
	}
	return s.linearFlow(ctx, id)
}
func (s *service) tickLinearFlows(ctx context.Context) error {
	rows, err := s.d.DB.QueryContext(ctx, `SELECT id,owner_id FROM workagents_linear_flows WHERE status='running' ORDER BY updated_at,created_at,id LIMIT 100`)
	if err != nil {
		return err
	}
	type pending struct{ id, owner string }
	var batch []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.owner); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	users, ok := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	if !ok {
		return errUnavailable
	}
	var failures []error
	for _, p := range batch {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		// Rotate even busy/erroring flows, so the oldest 100 cannot monopolize ticks.
		if _, e := s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET updated_at=? WHERE id=? AND status='running'`, db.FormatTime(s.now()), p.id); e != nil {
			failures = append(failures, fmt.Errorf("flow %s: %w", p.id, e))
			continue
		}
		u, e := users.Get(ctx, p.owner)
		if errors.Is(e, auth.ErrNotFound) || (e == nil && u.Status != auth.StatusActive) {
			if _, e = s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET status='failed',error_code='owner_unavailable',updated_at=? WHERE id=? AND status='running'`, db.FormatTime(s.now()), p.id); e != nil {
				failures = append(failures, fmt.Errorf("flow %s: %w", p.id, e))
			}
			continue
		}
		if e != nil {
			failures = append(failures, fmt.Errorf("flow %s owner: %w", p.id, e))
			continue
		}
		if e = s.advanceLinearFlow(auth.WithUser(ctx, u), p.id); e != nil {
			failures = append(failures, fmt.Errorf("flow %s: %w", p.id, e))
		}
	}
	return errors.Join(failures...)
}
func (s *service) advanceLinearFlow(ctx context.Context, id string) error {
	f, err := s.linearFlow(ctx, id)
	if err != nil {
		return err
	}
	if f.Status != "running" {
		return nil
	}
	fail := func(code string) error {
		_, e := s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET status='failed',error_code=?,updated_at=? WHERE id=? AND status='running' AND step=?`, code, db.FormatTime(s.now()), id, f.Step)
		return e
	}
	if !manager(ctx) {
		return fail("owner_permission_revoked")
	}
	if f.Step >= len(f.Plan.Steps) {
		_, err = s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET status='done',updated_at=? WHERE id=? AND status='running' AND step=?`, db.FormatTime(s.now()), id, f.Step)
		return err
	}
	step := f.Plan.Steps[f.Step]
	if step.Kind == "approval" {
		_, err = s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET status='awaiting_approval',updated_at=? WHERE id=? AND status='running' AND step=?`, db.FormatTime(s.now()), id, f.Step)
		return err
	}
	a, err := s.agent(ctx, step.AgentID)
	if err != nil || a.OwnerID != f.OwnerID || !a.Enabled || len(a.UnsupportedTools) > 0 {
		return fail("agent_unavailable")
	}
	input := f.Result
	if step.Kind == "draft" {
		if a.ExecutionKind != "content_draft" || f.ApprovedStep != f.Step-1 {
			return fail("approval_required")
		}
		data, _ := json.Marshal(map[string]string{"type": "post", "title": f.Plan.DraftTitle, "locale": f.Plan.DraftLocale, "body": f.Result})
		input = string(data)
	} else if a.ExecutionKind != "text" && !(f.Step == 0 && (a.ExecutionKind == "github_stars" || a.ExecutionKind == "github_starred")) {
		return fail("unsupported_step")
	}
	key := fmt.Sprintf("linear:%s:%d", id, f.Step)
	runID, err := s.run(ctx, a.ID, input, key)
	if errors.Is(err, errBusy) {
		return nil
	}
	if err != nil {
		return fail("step_queue_failed")
	}
	var state, output string
	if err = s.d.DB.QueryRowContext(ctx, `SELECT r.status,COALESCE(a.content,'') FROM workagents_runs r LEFT JOIN workagents_artifacts a ON a.run_id=r.id AND a.historical=0 WHERE r.id=?`, runID).Scan(&state, &output); err != nil {
		return err
	}
	if state == "failed" {
		return fail("step_failed")
	}
	if state != "done" {
		return nil
	}
	if strings.TrimSpace(output) == "" || len(output) > maxInputBytes {
		return fail("step_output_invalid")
	}
	_, err = s.d.DB.ExecContext(ctx, `UPDATE workagents_linear_flows SET step=step+1,result=?,updated_at=? WHERE id=? AND status='running' AND step=?`, output, db.FormatTime(s.now()), id, f.Step)
	return err
}

// Recheck at execution, since profiles and permissions can change after enqueue.
func (s *service) validateLinearRun(ctx context.Context, runID string, a Agent, input string) error {
	var key string
	if err := s.d.DB.QueryRowContext(ctx, `SELECT request_key FROM workagents_runs WHERE id=?`, runID).Scan(&key); err != nil {
		return err
	}
	if !strings.HasPrefix(key, "linear:") {
		return nil
	}
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return errInvalid
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		return errInvalid
	}
	f, err := s.linearFlow(ctx, parts[1])
	if err != nil {
		return err
	}
	if !manager(ctx) || f.OwnerID != owner(ctx) || a.OwnerID != f.OwnerID || f.Status != "running" || f.Step != index || index < 0 || index >= len(f.Plan.Steps) {
		return errUnavailable
	}
	step := f.Plan.Steps[index]
	if step.AgentID != a.ID {
		return errInvalid
	}
	expected := f.Result
	switch step.Kind {
	case "draft":
		if a.ExecutionKind != "content_draft" || f.ApprovedStep != index-1 {
			return errUnavailable
		}
		var hash string
		if err = s.d.DB.QueryRowContext(ctx, `SELECT approved_hash FROM workagents_linear_flows WHERE id=?`, f.ID).Scan(&hash); err != nil || hash != flowHash(f.Result) {
			return errUnavailable
		}
		data, _ := json.Marshal(map[string]string{"type": "post", "title": f.Plan.DraftTitle, "locale": f.Plan.DraftLocale, "body": f.Result})
		expected = string(data)
	case "agent":
		if a.ExecutionKind != "text" && !(index == 0 && (a.ExecutionKind == "github_stars" || a.ExecutionKind == "github_starred")) {
			return errUnavailable
		}
	default:
		return errInvalid
	}
	if expected != input {
		return errConflict
	}
	return nil
}
