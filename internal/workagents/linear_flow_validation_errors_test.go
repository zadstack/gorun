package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func insertFlow(t *testing.T, s *service, id, ownerID string, plan LinearPlan, step int, status, result string, approvedStep int, approvedHash string) {
	t.Helper()
	encoded, _ := json.Marshal(plan)
	if _, err := s.d.DB.Exec(`INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,step,status,result,approved_step,approved_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, ownerID, id, string(encoded), step, status, result, approvedStep, approvedHash, db.Now(), db.Now()); err != nil {
		t.Fatal(err)
	}
}

func flowState(t *testing.T, s *service, id string) (string, string) {
	t.Helper()
	var status, code string
	if err := s.d.DB.QueryRow(`SELECT status,error_code FROM workagents_linear_flows WHERE id=?`, id).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	return status, code
}

func TestLinearPlanValidationRejectsEveryUnsafeShape(t *testing.T) {
	s, ctx := setup(t, nil)
	text, err := s.saveAgent(ctx, Agent{Name: "Text", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	off, err := s.saveAgent(ctx, Agent{Name: "Off"})
	if err != nil {
		t.Fatal(err)
	}
	stars, err := s.saveAgent(ctx, Agent{Name: "Stars", Enabled: true, ExecutionKind: "github_stars"})
	if err != nil {
		t.Fatal(err)
	}
	_, other := customerCtx(t, s, "plan-other@example.com")
	foreign, err := s.saveAgent(ctx, Agent{Name: "Foreign", OwnerID: other.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	base := func(steps ...LinearStep) LinearPlan {
		return LinearPlan{Name: "Plan", Input: "Facts", RequestKey: "k", Steps: steps}
	}
	agent := func(id string) LinearStep { return LinearStep{Kind: "agent", AgentID: id} }
	approval := LinearStep{Kind: "approval"}
	cases := []struct {
		name string
		plan LinearPlan
		want error
	}{
		{"one step", base(agent(text.ID)), errInvalid},
		{"no request key", LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{agent(text.ID), approval}}, errInvalid},
		{"approval first", base(approval, agent(text.ID)), errInvalid},
		{"approval with agent", base(agent(text.ID), LinearStep{Kind: "approval", AgentID: text.ID}), errInvalid},
		{"double approval", base(agent(text.ID), approval, approval), errInvalid},
		{"missing agent", base(agent("missing"), approval), errNotFound},
		{"disabled agent", base(agent(off.ID), approval), errUnavailable},
		{"foreign owner", base(agent(foreign.ID), approval), errUnavailable},
		{"tool not first", base(agent(text.ID), agent(stars.ID), approval), errUnavailable},
		{"unknown kind", base(agent(text.ID), LinearStep{Kind: "branch"}), errInvalid},
		{"no approval", base(agent(text.ID), agent(text.ID)), errInvalid},
	}
	for _, c := range cases {
		if _, err := s.startLinearFlow(ctx, c.plan); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	customer, _ := customerCtx(t, s, "plan-customer@example.com")
	if _, err = s.startLinearFlow(customer, base(agent(text.ID), approval)); !errors.Is(err, errUnavailable) {
		t.Fatal("customer started flow", err)
	}
	var n int
	if err = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_linear_flows`).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejected plans stored", n, err)
	}
}

func TestLinearFlowReadAndDecisionFailures(t *testing.T) {
	s, ctx := setup(t, nil)
	if _, err := s.linearFlow(ctx, "missing"); !errors.Is(err, errNotFound) {
		t.Fatal(err)
	}
	if _, err := s.d.DB.Exec(`INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,created_at,updated_at) VALUES('bad-plan',?,'bad','[]',?,?)`, owner(ctx), db.Now(), db.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.linearFlow(ctx, "bad-plan"); !errors.Is(err, errInvalid) {
		t.Fatal("undecodable plan accepted", err)
	}
	customer, _ := customerCtx(t, s, "decide@example.com")
	if _, err := s.decideLinearFlow(customer, "bad-plan", LinearDecision{}); !errors.Is(err, errUnavailable) {
		t.Fatal("customer decided", err)
	}
	if _, err := s.decideLinearFlow(ctx, "missing", LinearDecision{}); !errors.Is(err, errNotFound) {
		t.Fatal(err)
	}
	plan := LinearPlan{Name: "P", Input: "I", RequestKey: "k", Steps: []LinearStep{{Kind: "agent", AgentID: "a"}, {Kind: "approval"}}}
	insertFlow(t, s, "gate", owner(ctx), plan, 1, "awaiting_approval", "output", -1, "")
	decision := LinearDecision{Approve: true, Step: 1, ResultHash: flowHash("output")}

	// A concurrent writer that wins the row makes the guarded update a no-op.
	if _, err := s.d.DB.Exec(`CREATE TRIGGER skip_decision BEFORE UPDATE ON workagents_linear_flows BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.decideLinearFlow(ctx, "gate", decision); !errors.Is(err, errConflict) {
		t.Fatal("lost race not reported as conflict", err)
	}
	if _, err := s.d.DB.Exec(`DROP TRIGGER skip_decision`); err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "no_decision", "UPDATE", "workagents_linear_flows", "")
	if _, err := s.decideLinearFlow(ctx, "gate", decision); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("decision storage failure hidden", err)
	}
	if status, _ := flowState(t, s, "gate"); status != "awaiting_approval" {
		t.Fatal("failed decision changed state", status)
	}
}

func TestLinearFlowStartSurfacesStorageFailure(t *testing.T) {
	s, ctx := setup(t, nil)
	a, err := s.saveAgent(ctx, Agent{Name: "Writer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "no_flow", "INSERT", "workagents_linear_flows", "")
	if _, err = s.startLinearFlow(ctx, LinearPlan{Name: "P", Input: "I", RequestKey: "k", Steps: []LinearStep{{Kind: "agent", AgentID: a.ID}, {Kind: "approval"}}}); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("flow insert failure hidden", err)
	}
}

func TestAdvanceLinearFlowFailsClosedOnChangedProfilesAndOutputs(t *testing.T) {
	var output string
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return output, nil
	})
	text, err := s.saveAgent(ctx, Agent{Name: "Text", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stars, err := s.saveAgent(ctx, Agent{Name: "Stars", Enabled: true, ExecutionKind: "github_stars"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.saveAgent(ctx, Agent{Name: "Draft", Enabled: true, ExecutionKind: "content_draft"})
	if err != nil {
		t.Fatal(err)
	}
	off, err := s.saveAgent(ctx, Agent{Name: "Off"})
	if err != nil {
		t.Fatal(err)
	}
	me := owner(ctx)
	steps := func(st ...LinearStep) LinearPlan { return LinearPlan{Name: "P", Input: "I", Steps: st} }
	approval := LinearStep{Kind: "approval"}

	// Non-running flows are left alone.
	insertFlow(t, s, "waiting", me, steps(LinearStep{Kind: "agent", AgentID: text.ID}, approval), 1, "awaiting_approval", "x", -1, "")
	if err = s.advanceLinearFlow(ctx, "waiting"); err != nil {
		t.Fatal(err)
	}
	if status, _ := flowState(t, s, "waiting"); status != "awaiting_approval" {
		t.Fatal("non-running flow advanced", status)
	}

	cases := []struct {
		id   string
		plan LinearPlan
		step int
		code string
	}{
		{"disabled-agent", steps(LinearStep{Kind: "agent", AgentID: off.ID}, approval), 0, "agent_unavailable"},
		{"draft-unapproved", steps(LinearStep{Kind: "agent", AgentID: text.ID}, approval, LinearStep{Kind: "draft", AgentID: draft.ID}), 2, "approval_required"},
		{"tool-later", steps(LinearStep{Kind: "agent", AgentID: text.ID}, LinearStep{Kind: "agent", AgentID: stars.ID}, approval), 1, "unsupported_step"},
	}
	for _, c := range cases {
		insertFlow(t, s, c.id, me, c.plan, c.step, "running", "prior", -1, "")
		if err = s.advanceLinearFlow(ctx, c.id); err != nil {
			t.Fatal(c.id, err)
		}
		if status, code := flowState(t, s, c.id); status != "failed" || code != c.code {
			t.Fatal(c.id, status, code)
		}
	}

	// Queue refusal (input too large for a run) fails the flow.
	insertFlow(t, s, "queue-fail", me, steps(LinearStep{Kind: "agent", AgentID: text.ID}, approval), 0, "running", strings.Repeat("x", maxInputBytes+1), -1, "")
	if err = s.advanceLinearFlow(ctx, "queue-fail"); err != nil {
		t.Fatal(err)
	}
	if status, code := flowState(t, s, "queue-fail"); status != "failed" || code != "step_queue_failed" {
		t.Fatal(status, code)
	}

	// Step run failed / produced unusable output.
	run := func(id string) {
		t.Helper()
		insertFlow(t, s, id, me, steps(LinearStep{Kind: "agent", AgentID: text.ID}, approval), 0, "running", "input "+id, -1, "")
		if err := s.advanceLinearFlow(ctx, id); err != nil {
			t.Fatal(err)
		}
		runs, err := s.runs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range runs {
			if r.Status == "queued" {
				_ = execRun(s, ctx, r.ID)
			}
		}
		if err := s.advanceLinearFlow(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	output = ""
	run("step-failed")
	if status, code := flowState(t, s, "step-failed"); status != "failed" || code != "step_failed" {
		t.Fatal(status, code)
	}
	output = strings.Repeat("y", maxInputBytes+1)
	run("step-too-long")
	if status, code := flowState(t, s, "step-too-long"); status != "failed" || code != "step_output_invalid" {
		t.Fatal(status, code)
	}

	// A full per-owner queue defers without failing.
	for i := 0; i < 3; i++ {
		if _, err = s.run(ctx, text.ID, "filler", ""); err != nil {
			t.Fatal(err)
		}
	}
	insertFlow(t, s, "busy", me, steps(LinearStep{Kind: "agent", AgentID: text.ID}, approval), 0, "running", "wait", -1, "")
	if err = s.advanceLinearFlow(ctx, "busy"); err != nil {
		t.Fatal(err)
	}
	if status, _ := flowState(t, s, "busy"); status != "running" {
		t.Fatal("busy queue failed the flow", status)
	}
}

func TestAdvanceLinearFlowRevokedOwnerPermission(t *testing.T) {
	s, _ := setup(t, nil)
	customer, u := customerCtx(t, s, "revoked@example.com")
	insertFlow(t, s, "revoked", u.ID, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: "a"}, {Kind: "approval"}}}, 0, "running", "x", -1, "")
	if err := s.advanceLinearFlow(customer, "revoked"); err != nil {
		t.Fatal(err)
	}
	if status, code := flowState(t, s, "revoked"); status != "failed" || code != "owner_permission_revoked" {
		t.Fatal(status, code)
	}
}

func TestTickLinearFlowsFailurePaths(t *testing.T) {
	t.Run("no auth service", func(t *testing.T) {
		s, ctx := setup(t, nil)
		s.d.Services = kernel.NewServiceRegistry()
		if err := s.tickLinearFlows(ctx); !errors.Is(err, errUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		s, ctx := setup(t, nil)
		insertFlow(t, s, "pending", owner(ctx), LinearPlan{Steps: []LinearStep{}}, 0, "running", "", -1, "")
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		// The query itself may observe cancellation first; either way no work is done.
		if err := s.tickLinearFlows(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation not reported", err)
		}
		if status, _ := flowState(t, s, "pending"); status != "running" {
			t.Fatal("cancelled tick advanced", status)
		}
	})
	t.Run("rotate failure", func(t *testing.T) {
		s, ctx := setup(t, nil)
		insertFlow(t, s, "stuck", owner(ctx), LinearPlan{Steps: []LinearStep{}}, 0, "running", "", -1, "")
		abortOn(t, s, "no_rotate", "UPDATE", "workagents_linear_flows", "")
		if err := s.tickLinearFlows(ctx); err == nil || !strings.Contains(err.Error(), "flow stuck") {
			t.Fatal("rotate failure hidden", err)
		}
	})
	t.Run("inactive owner failure", func(t *testing.T) {
		s, ctx := setup(t, nil)
		insertFlow(t, s, "orphan", owner(ctx), LinearPlan{Steps: []LinearStep{}}, 0, "running", "", -1, "")
		if _, err := s.d.DB.Exec(`UPDATE users SET status='disabled' WHERE id=?`, owner(ctx)); err != nil {
			t.Fatal(err)
		}
		abortOn(t, s, "no_fail", "UPDATE", "workagents_linear_flows", "NEW.status='failed'")
		if err := s.tickLinearFlows(ctx); err == nil || !strings.Contains(err.Error(), "flow orphan") {
			t.Fatal("owner failure write hidden", err)
		}
	})
}

func TestValidateLinearRunRejectsForgedOrStaleRuns(t *testing.T) {
	s, ctx := setup(t, nil)
	text, err := s.saveAgent(ctx, Agent{Name: "Text", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.saveAgent(ctx, Agent{Name: "Other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stars, err := s.saveAgent(ctx, Agent{Name: "Stars", Enabled: true, ExecutionKind: "github_stars"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.saveAgent(ctx, Agent{Name: "Draft", Enabled: true, ExecutionKind: "content_draft"})
	if err != nil {
		t.Fatal(err)
	}
	me := owner(ctx)
	approval := LinearStep{Kind: "approval"}
	insertFlow(t, s, "f1", me, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: text.ID}, approval, {Kind: "agent", AgentID: stars.ID}}}, 0, "running", "expected", -1, "")
	insertFlow(t, s, "f2", me, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: text.ID}, approval, {Kind: "draft", AgentID: draft.ID}}, DraftTitle: "T", DraftLocale: "en"}, 2, "running", "body", 1, "wrong-hash")
	insertFlow(t, s, "f3", me, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: text.ID}, approval, {Kind: "draft", AgentID: draft.ID}}}, 2, "running", "body", 0, "")
	insertFlow(t, s, "f4", me, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: text.ID}, {Kind: "approval", AgentID: text.ID}}}, 1, "running", "x", -1, "")
	insertFlow(t, s, "f5", me, LinearPlan{Name: "P", Input: "I", Steps: []LinearStep{{Kind: "agent", AgentID: text.ID}, approval, {Kind: "agent", AgentID: stars.ID}}}, 2, "running", "x", -1, "")
	key := func(id, k string) {
		t.Helper()
		if _, err := s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,request_key,input,created_at)VALUES(?,?,?,?,'x',?)`, id, me, text.ID, k, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	key("r-parts", "linear:f1")
	key("r-index", "linear:f1:abc")
	key("r-missing", "linear:missing:0")
	key("r-step", "linear:f1:1")
	key("r-agent", "linear:f1:0")
	key("r-input", "linear:f1:0x")
	key("r-hash", "linear:f2:2")
	key("r-unapproved", "linear:f3:2")
	key("r-kind", "linear:f4:1")
	key("r-tool", "linear:f5:2")
	cases := []struct {
		run   string
		agent Agent
		input string
		want  error
	}{
		{"missing-run", text, "x", nil},
		{"r-parts", text, "x", errInvalid},
		{"r-index", text, "x", errInvalid},
		{"r-input", text, "x", errInvalid},
		{"r-missing", text, "x", errNotFound},
		{"r-step", text, "x", errUnavailable},
		{"r-agent", other, "expected", errInvalid},
		{"r-hash", draft, "x", errUnavailable},
		{"r-unapproved", draft, "x", errUnavailable},
		{"r-kind", text, "x", errInvalid},
		{"r-tool", stars, "x", errUnavailable},
	}
	for _, c := range cases {
		err := s.validateLinearRun(ctx, c.run, c.agent, c.input)
		if c.want == nil {
			if err == nil {
				t.Fatal("unknown run validated", c.run)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.run, err, c.want)
		}
	}
	// Correct step and agent but different input than the flow's stored result.
	if err = s.validateLinearRun(ctx, "r-agent", text, "tampered"); !errors.Is(err, errConflict) {
		t.Fatal("tampered input accepted", err)
	}
	if err = s.validateLinearRun(ctx, "r-agent", text, "expected"); err != nil {
		t.Fatal("matching run rejected", err)
	}
}
