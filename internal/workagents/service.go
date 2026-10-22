// Package workagents runs owner-scoped text agents through GoRun's job queue.
package workagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

const jobKind = "workagents.run"
const maxInputBytes = 32 << 10
const maxOutputBytes = 1 << 20

var errBusy = errors.New("agent queue is full for this account; wait for a running task to finish")

var errInvalid = errors.New("invalid agent request")
var errUnavailable = errors.New("agent unavailable or unsupported tools required")
var errNotFound = errors.New("agent record not found")
var errConflict = errors.New("request key already used for different input")

// Completion is supplied by the native provider adapter; secrets never enter
// this module's database or HTTP responses.
type Completion func(context.Context, *kernel.Deps, string, string, string, string) (string, error)

type Agent struct {
	ID                 string   `json:"id"`
	OwnerID            string   `json:"owner_id"`
	SourceID           string   `json:"source_id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	SystemPrompt       string   `json:"system_prompt"`
	ConnectionID       string   `json:"connection_id"`
	Model              string   `json:"model"`
	Enabled            bool     `json:"enabled"`
	UnsupportedTools   []string `json:"unsupported_tools"`
	ProviderConfigured bool     `json:"provider_configured"`
	ExecutionKind      string   `json:"execution_kind"`
}

type Run struct {
	ID         string `json:"id"`
	AgentID    string `json:"agent_id"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code"`
	Input      string `json:"input"`
	Result     string `json:"result"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at"`
	Historical bool   `json:"historical"`
}

type Workflow struct {
	ID              string `json:"id"`
	AgentID         string `json:"agent_id"`
	Name            string `json:"name"`
	Input           string `json:"input"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	NextRunAt       string `json:"next_run_at"`
	BlockedReason   string `json:"blocked_reason,omitempty"`
}

type service struct {
	d              *kernel.Deps
	complete       Completion
	reviewComplete func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error)
	now            func() time.Time
	githubClient   *http.Client
}

func manager(ctx context.Context) bool { return auth.Can(ctx, "workagents.manage") }
func owner(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil && u.Status == auth.StatusActive {
		return u.ID
	}
	return ""
}

func (s *service) agent(ctx context.Context, id string) (Agent, error) {
	var a Agent
	var tools string
	e := s.d.DB.QueryRowContext(ctx, `SELECT id,owner_id,source_id,name,description,system_prompt,connection_id,model,enabled,unsupported_tools,execution_kind FROM workagents_agents WHERE id=? AND (owner_id=? OR ?=1)`, id, owner(ctx), manager(ctx)).Scan(&a.ID, &a.OwnerID, &a.SourceID, &a.Name, &a.Description, &a.SystemPrompt, &a.ConnectionID, &a.Model, &a.Enabled, &tools, &a.ExecutionKind)
	if errors.Is(e, sql.ErrNoRows) {
		return a, errNotFound
	}
	if e != nil {
		return a, e
	}
	if json.Unmarshal([]byte(tools), &a.UnsupportedTools) != nil {
		return a, errUnavailable
	}
	a.ProviderConfigured = a.ExecutionKind != "text" || (s.complete != nil && s.d.Settings != nil && admin.WorkAgentProviderReady(ctx, s.d, a.ConnectionID, a.Model))
	return a, nil
}

func (s *service) agents(ctx context.Context) ([]Agent, error) {
	rows, e := s.d.DB.QueryContext(ctx, `SELECT id FROM workagents_agents WHERE owner_id=? OR ?=1 ORDER BY name LIMIT 200`, owner(ctx), manager(ctx))
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := make([]Agent, 0, len(ids))
	for _, id := range ids {
		a, e := s.agent(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *service) saveAgent(ctx context.Context, a Agent) (Agent, error) {
	if !manager(ctx) || owner(ctx) == "" {
		return a, errUnavailable
	}
	if a.ExecutionKind == "" {
		a.ExecutionKind = "text"
	}
	if a.ExecutionKind != "text" && a.ExecutionKind != "github_stars" && a.ExecutionKind != "github_starred" && a.ExecutionKind != "content_draft" {
		return a, errInvalid
	}
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 160 || len(a.Description) > 4096 || len(a.SystemPrompt) > maxInputBytes || len(a.Model) > 160 || len(a.ConnectionID) > 26 || len(a.UnsupportedTools) > 40 {
		return a, errInvalid
	}
	if a.ID != "" {
		old, e := s.agent(ctx, a.ID)
		if e != nil {
			return a, e
		}
		if a.OwnerID != "" && a.OwnerID != old.OwnerID {
			return a, errInvalid
		}
		a.OwnerID = old.OwnerID
		a.SourceID = old.SourceID
	} else {
		a.ID = db.NewID()
		if a.OwnerID == "" {
			a.OwnerID = owner(ctx)
		}
		users, ok := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
		if !ok {
			return a, errUnavailable
		}
		u, e := users.Get(ctx, a.OwnerID)
		if e != nil || u.Status != auth.StatusActive {
			return a, errInvalid
		}
		a.SourceID = ""
	}
	if a.UnsupportedTools == nil {
		a.UnsupportedTools = []string{}
	}
	tools, _ := json.Marshal(a.UnsupportedTools)
	_, e := s.d.DB.ExecContext(ctx, `INSERT INTO workagents_agents(id,owner_id,source_id,name,description,system_prompt,connection_id,model,enabled,unsupported_tools,created_at,updated_at,execution_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,system_prompt=excluded.system_prompt,connection_id=excluded.connection_id,model=excluded.model,enabled=excluded.enabled,unsupported_tools=excluded.unsupported_tools,updated_at=excluded.updated_at,execution_kind=excluded.execution_kind`, a.ID, a.OwnerID, a.SourceID, a.Name, a.Description, a.SystemPrompt, a.ConnectionID, a.Model, a.Enabled, string(tools), db.FormatTime(s.now()), db.FormatTime(s.now()), a.ExecutionKind)
	if e == nil && s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.profile.saved", "workagent", a.ID, nil)
	}
	if e != nil {
		return a, e
	}
	return s.agent(ctx, a.ID)
}

func (s *service) run(ctx context.Context, id, input, key string) (string, error) {
	a, e := s.agent(ctx, id)
	if e != nil {
		return "", e
	}
	if !a.Enabled || len(a.UnsupportedTools) > 0 {
		return "", errUnavailable
	}
	if strings.TrimSpace(input) == "" || len(input) > maxInputBytes || len(key) > 160 {
		return "", errInvalid
	}
	var runID string
	limit := s.pendingLimit(ctx)
	e = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		if key != "" {
			var oldAgent, oldInput string
			e := tx.QueryRowContext(ctx, `SELECT id,agent_id,input FROM workagents_runs WHERE owner_id=? AND request_key=?`, a.OwnerID, key).Scan(&runID, &oldAgent, &oldInput)
			if e == nil {
				if oldAgent != id || oldInput != input {
					return errConflict
				}
				return nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		var pending int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_runs WHERE owner_id=? AND historical=0 AND status IN ('queued','running')`, a.OwnerID).Scan(&pending); e != nil {
			return e
		}
		if pending >= limit {
			return errBusy
		}
		runID = db.NewID()
		jobID, e := s.d.Jobs.EnqueueTx(ctx, tx, jobKind, map[string]string{"run_id": runID}, jobs.Idempotency("workagents:"+runID))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO workagents_runs(id,owner_id,agent_id,job_id,request_key,input,created_at) VALUES(?,?,?,?,?,?,?)`, runID, a.OwnerID, id, jobID, key, input, db.FormatTime(s.now()))
		return e
	})
	if e == nil && s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.run.queued", "workagent_run", runID, nil)
	}
	return runID, e
}

func (s *service) runs(ctx context.Context) ([]Run, error) {
	rows, e := s.d.DB.QueryContext(ctx, `SELECT r.id,r.agent_id,r.workflow_id,r.status,r.error_code,r.input,COALESCE((SELECT content FROM workagents_artifacts a WHERE a.run_id=r.id AND a.historical=0 LIMIT 1),''),r.created_at,r.finished_at,r.historical FROM workagents_runs r WHERE r.owner_id=? OR ?=1 ORDER BY r.created_at DESC LIMIT 100`, owner(ctx), manager(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if e = rows.Scan(&r.ID, &r.AgentID, &r.WorkflowID, &r.Status, &r.ErrorCode, &r.Input, &r.Result, &r.CreatedAt, &r.FinishedAt, &r.Historical); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *service) execute(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(payload, &p) != nil || p.RunID == "" {
		return errInvalid
	}
	var input, agentID, ownerID, workflowID, status string
	var historical bool
	e := s.d.DB.QueryRowContext(ctx, `SELECT input,agent_id,owner_id,workflow_id,status,historical FROM workagents_runs WHERE id=?`, p.RunID).Scan(&input, &agentID, &ownerID, &workflowID, &status, &historical)
	if e != nil {
		return e
	}
	if historical || status == "done" || status == "failed" {
		return nil
	}
	users, ok := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	if !ok {
		return s.fail(ctx, p.RunID, "owner_unavailable")
	}
	u, e := users.Get(ctx, ownerID)
	if e != nil || u.Status != auth.StatusActive {
		return s.fail(ctx, p.RunID, "owner_unavailable")
	}
	ctx = auth.WithUser(ctx, u)
	a, e := s.agent(ctx, agentID)
	if e != nil || !a.Enabled || len(a.UnsupportedTools) > 0 {
		return s.fail(ctx, p.RunID, "unsupported_or_disabled")
	}
	if workflowID != "" {
		if workflowBlockedReason(input) != "" {
			return s.fail(ctx, p.RunID, "workflow_graph_unsupported")
		}
		var enabled bool
		if s.d.DB.QueryRowContext(ctx, `SELECT enabled FROM workagents_workflows WHERE id=?`, workflowID).Scan(&enabled) != nil || !enabled {
			return s.fail(ctx, p.RunID, "workflow_disabled")
		}
	}
	if err := s.validateLinearRun(ctx, p.RunID, a, input); err != nil {
		return s.fail(ctx, p.RunID, "linear_context_invalid")
	}
	if a.ExecutionKind == "text" && s.complete == nil {
		return s.fail(ctx, p.RunID, "ai_unconfigured")
	}
	if _, e = s.d.DB.ExecContext(ctx, `UPDATE workagents_runs SET status='running',started_at=? WHERE id=?`, db.FormatTime(s.now()), p.RunID); e != nil {
		return e
	}
	var result string
	switch a.ExecutionKind {
	case "text":
		result, e = s.complete(ctx, s.d, a.ConnectionID, a.Model, a.SystemPrompt, input)
	case "github_stars":
		result, e = s.githubStars(ctx, input)
	case "github_starred":
		result, e = s.githubStarred(ctx, input)
	case "content_draft":
		result, e = s.contentDraft(ctx, p.RunID, input)
	default:
		e = errUnavailable
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		code := "provider_failed"
		var failure toolError
		if errors.As(e, &failure) {
			code = string(failure)
		}
		return s.fail(ctx, p.RunID, code)
	}
	if strings.TrimSpace(result) == "" || len(result) > maxOutputBytes {
		return s.fail(ctx, p.RunID, "invalid_provider_result")
	}
	e = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		now := db.FormatTime(s.now())
		if _, e := tx.ExecContext(ctx, `INSERT INTO workagents_artifacts(id,owner_id,run_id,name,content,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, db.NewID(), ownerID, p.RunID, a.Name, result, now); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE workagents_runs SET status='done',error_code='',finished_at=? WHERE id=?`, now, p.RunID)
		return e
	})
	if e == nil {
		if s.d.Audit != nil {
			s.d.Audit.Record(ctx, "workagent.run.finished", "workagent_run", p.RunID, nil)
		}
		s.d.Log.Info("workagent run finished", "run_id", p.RunID, "agent_id", a.ID)
	}
	return e
}

func (s *service) fail(ctx context.Context, id, code string) error {
	_, e := s.d.DB.ExecContext(ctx, `UPDATE workagents_runs SET status='failed',error_code=?,finished_at=? WHERE id=?`, code, db.FormatTime(s.now()), id)
	if e != nil {
		return e
	}
	s.d.Log.Warn("workagent run failed", "run_id", id, "error_code", code)
	if s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.run.failed", "workagent_run", id, map[string]string{"error_code": code})
	}
	return errors.New(code)
}

func (s *service) workflows(ctx context.Context) ([]Workflow, error) {
	rows, e := s.d.DB.QueryContext(ctx, `SELECT id,agent_id,name,input,enabled,interval_seconds,next_run_at FROM workagents_workflows WHERE owner_id=? OR ?=1 ORDER BY name LIMIT 200`, owner(ctx), manager(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		var w Workflow
		if e = rows.Scan(&w.ID, &w.AgentID, &w.Name, &w.Input, &w.Enabled, &w.IntervalSeconds, &w.NextRunAt); e != nil {
			return nil, e
		}
		w.BlockedReason = workflowBlockedReason(w.Input)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *service) saveWorkflow(ctx context.Context, w Workflow) (Workflow, error) {
	a, e := s.agent(ctx, w.AgentID)
	if e != nil {
		return w, e
	}
	if !manager(ctx) {
		return w, errUnavailable
	}
	if w.Enabled && workflowBlockedReason(w.Input) != "" {
		return w, errWorkflowGraph
	}
	if w.Enabled && (!a.Enabled || len(a.UnsupportedTools) > 0) {
		return w, errUnavailable
	}
	if w.IntervalSeconds < 60 || w.IntervalSeconds > 31536000 || strings.TrimSpace(w.Input) == "" || len(w.Input) > maxInputBytes || len(w.Name) > 160 || strings.TrimSpace(w.Name) == "" {
		return w, errInvalid
	}
	now := s.now()
	if w.ID == "" {
		w.ID = db.NewID()
	} else {
		var agentID string
		if s.d.DB.QueryRowContext(ctx, `SELECT agent_id FROM workagents_workflows WHERE id=? AND (owner_id=? OR ?=1)`, w.ID, owner(ctx), manager(ctx)).Scan(&agentID) != nil {
			return w, errNotFound
		}
		if agentID != w.AgentID {
			return w, errInvalid
		}
	}
	w.NextRunAt = db.FormatTime(now.Add(time.Duration(w.IntervalSeconds) * time.Second))
	_, e = s.d.DB.ExecContext(ctx, `INSERT INTO workagents_workflows(id,owner_id,agent_id,name,input,enabled,interval_seconds,next_run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,input=excluded.input,enabled=excluded.enabled,interval_seconds=excluded.interval_seconds,next_run_at=excluded.next_run_at,updated_at=excluded.updated_at`, w.ID, a.OwnerID, a.ID, w.Name, w.Input, w.Enabled, w.IntervalSeconds, w.NextRunAt, db.FormatTime(now), db.FormatTime(now))
	if e == nil && s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.schedule.saved", "workagent_workflow", w.ID, nil)
	}
	return w, e
}

func (s *service) schedule(ctx context.Context) error {
	limit := s.pendingLimit(ctx)
	return db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		now := s.now()
		rows, e := tx.QueryContext(ctx, `SELECT w.id,w.owner_id,w.agent_id,w.input,w.interval_seconds FROM workagents_workflows w JOIN users u ON u.id=w.owner_id JOIN workagents_agents a ON a.id=w.agent_id WHERE w.enabled=1 AND w.interval_seconds>=60 AND w.next_run_at<=? AND u.status='active' AND a.enabled=1 AND json_array_length(a.unsupported_tools)=0 LIMIT 100`, db.FormatTime(now))
		if e != nil {
			return e
		}
		type due struct {
			id, owner, agent, input string
			interval                int
		}
		var pending []due
		for rows.Next() {
			var p due
			if e = rows.Scan(&p.id, &p.owner, &p.agent, &p.input, &p.interval); e != nil {
				rows.Close()
				return e
			}
			pending = append(pending, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, p := range pending {
			if workflowBlockedReason(p.input) != "" {
				continue
			}
			var n int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_runs WHERE owner_id=? AND historical=0 AND status IN ('queued','running')`, p.owner).Scan(&n); e != nil {
				return e
			}
			if n >= limit {
				continue
			}
			runID := db.NewID()
			jobID, e := s.d.Jobs.EnqueueTx(ctx, tx, jobKind, map[string]string{"run_id": runID}, jobs.Idempotency("workagents:"+runID))
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO workagents_runs(id,owner_id,agent_id,workflow_id,job_id,input,created_at) VALUES(?,?,?,?,?,?,?)`, runID, p.owner, p.agent, p.id, jobID, p.input, db.FormatTime(now)); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE workagents_workflows SET next_run_at=?,updated_at=? WHERE id=?`, db.FormatTime(now.Add(time.Duration(p.interval)*time.Second)), db.FormatTime(now), p.id); e != nil {
				return e
			}
			if s.d.Audit != nil {
				s.d.Audit.RecordTx(ctx, tx, "workagent.schedule.queued", "workagent_run", runID, map[string]string{"workflow_id": p.id})
			}
		}
		return nil
	})
}

func (s *service) pendingLimit(ctx context.Context) int {
	limit := 3
	if s.d.Settings != nil {
		var configured int
		if s.d.Settings.Get(ctx, "workagents.max_pending_per_owner", &configured) == nil {
			limit = configured
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 20 {
		return 20
	}
	return limit
}
