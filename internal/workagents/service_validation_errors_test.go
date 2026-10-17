package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// abortOn installs a trigger that makes the given write fail, so storage
// failures can be asserted without closing the database.
func abortOn(t *testing.T, s *service, name, event, table, when string) {
	t.Helper()
	if when != "" {
		when = " WHEN " + when
	}
	if _, err := s.d.DB.Exec(`CREATE TRIGGER ` + name + ` BEFORE ` + event + ` ON ` + table + ` FOR EACH ROW` + when + ` BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
}

func customerCtx(t *testing.T, s *service, email string) (context.Context, *auth.User) {
	t.Helper()
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	u, err := users.Create(context.Background(), email, "Customer", "correct horse battery", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	return auth.WithUser(context.Background(), u), u
}

func runStatus(t *testing.T, s *service, id string) (string, string) {
	t.Helper()
	var status, code string
	if err := s.d.DB.QueryRow(`SELECT status,error_code FROM workagents_runs WHERE id=?`, id).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	return status, code
}

func execRun(s *service, ctx context.Context, id string) error {
	b, _ := json.Marshal(map[string]string{"run_id": id})
	return s.execute(ctx, b)
}

func TestSaveAgentRejectsInvalidProfilesAndUnauthorisedCallers(t *testing.T) {
	s, ctx := setup(t, nil)
	customer, _ := customerCtx(t, s, "c1@example.com")
	if _, err := s.saveAgent(customer, Agent{Name: "Escalation"}); !errors.Is(err, errUnavailable) {
		t.Fatal("customer saved profile", err)
	}
	for _, a := range []Agent{
		{Name: "Bad kind", ExecutionKind: "shell"},
		{Name: "   "},
		{Name: strings.Repeat("n", 161)},
		{Name: "Long connection", ConnectionID: strings.Repeat("c", 27)},
		{Name: "Unknown owner", OwnerID: "no-such-user"},
	} {
		if _, err := s.saveAgent(ctx, a); !errors.Is(err, errInvalid) {
			t.Fatalf("invalid profile %+v accepted: %v", a, err)
		}
	}
	if _, err := s.saveAgent(ctx, Agent{ID: "missing", Name: "Ghost"}); !errors.Is(err, errNotFound) {
		t.Fatal("update of unknown profile", err)
	}
	a, err := s.saveAgent(ctx, Agent{Name: "Owned"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveAgent(ctx, Agent{ID: a.ID, OwnerID: "someone-else", Name: "Moved"}); !errors.Is(err, errInvalid) {
		t.Fatal("owner reassigned on update", err)
	}
	if _, err = s.d.DB.Exec(`UPDATE workagents_agents SET unsupported_tools='{}' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.agent(ctx, a.ID); !errors.Is(err, errUnavailable) {
		t.Fatal("corrupt tool list accepted", err)
	}
	if _, err = s.agents(ctx); !errors.Is(err, errUnavailable) {
		t.Fatal("agents list ignored corrupt record", err)
	}
	s.d.Services = kernel.NewServiceRegistry()
	if _, err = s.saveAgent(ctx, Agent{Name: "No auth service"}); !errors.Is(err, errUnavailable) {
		t.Fatal("profile saved without auth service", err)
	}
}

func TestSaveAgentAndListsSurfaceStorageFailures(t *testing.T) {
	s, ctx := setup(t, nil)
	abortOn(t, s, "no_agent_insert", "INSERT", "workagents_agents", "")
	if _, err := s.saveAgent(ctx, Agent{Name: "Blocked"}); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("insert failure hidden", err)
	}
	s.d.DB.Close()
	if _, err := s.agent(ctx, "x"); err == nil || errors.Is(err, errNotFound) {
		t.Fatal("closed database reported as not found", err)
	}
	if _, err := s.agents(ctx); err == nil {
		t.Fatal("agents on closed db")
	}
	if _, err := s.runs(ctx); err == nil {
		t.Fatal("runs on closed db")
	}
	if _, err := s.workflows(ctx); err == nil {
		t.Fatal("workflows on closed db")
	}
	if _, err := s.linearFlow(ctx, "x"); err == nil || errors.Is(err, errNotFound) {
		t.Fatal("linear flow on closed db", err)
	}
	if err := s.tickLinearFlows(ctx); err == nil {
		t.Fatal("tick on closed db")
	}
}

func TestRunRejectsInvalidInputAndQueueFailures(t *testing.T) {
	s, ctx := setup(t, nil)
	a, err := s.saveAgent(ctx, Agent{Name: "Runner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ input, key string }{{"  ", ""}, {strings.Repeat("x", maxInputBytes+1), ""}, {"ok", strings.Repeat("k", 161)}} {
		if _, err = s.run(ctx, a.ID, c.input, c.key); !errors.Is(err, errInvalid) {
			t.Fatal("invalid run accepted", len(c.input), len(c.key), err)
		}
	}
	abortOn(t, s, "no_jobs", "INSERT", "jobs", "")
	if _, err = s.run(ctx, a.ID, "queued", ""); err == nil {
		t.Fatal("enqueue failure hidden")
	}
	var n int
	if err = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatal("run persisted without a job", n, err)
	}
}

func TestExecuteFailsClosedForInvalidRunsOwnersAndWorkflows(t *testing.T) {
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return "ok", nil
	})
	if err := s.execute(ctx, json.RawMessage(`{"run_id":""}`)); !errors.Is(err, errInvalid) {
		t.Fatal("empty run id", err)
	}
	if err := s.execute(ctx, json.RawMessage(`nope`)); !errors.Is(err, errInvalid) {
		t.Fatal("bad payload", err)
	}
	if err := execRun(s, ctx, "missing"); err == nil {
		t.Fatal("unknown run executed")
	}
	a, err := s.saveAgent(ctx, Agent{Name: "Exec", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queue := func(input string) string {
		t.Helper()
		id, err := s.run(ctx, a.ID, input, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Disabled owner.
	disabledOwner := queue("owner check")
	if _, err = s.d.DB.Exec(`UPDATE users SET status='disabled' WHERE id=?`, a.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, ctx, disabledOwner); err == nil || err.Error() != "owner_unavailable" {
		t.Fatal("disabled owner executed", err)
	}
	if _, err = s.d.DB.Exec(`UPDATE users SET status='active' WHERE id=?`, a.OwnerID); err != nil {
		t.Fatal(err)
	}

	// Agent disabled after queueing.
	disabledAgent := queue("agent check")
	if _, err = s.d.DB.Exec(`UPDATE workagents_agents SET enabled=0 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, ctx, disabledAgent); err == nil || err.Error() != "unsupported_or_disabled" {
		t.Fatal("disabled agent executed", err)
	}
	if _, err = s.d.DB.Exec(`UPDATE workagents_agents SET enabled=1 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}

	// Workflow runs re-check graph input and enabled state at execution.
	w, err := s.saveWorkflow(ctx, Workflow{AgentID: a.ID, Name: "Paused", Input: "x", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	insertWorkflowRun := func(id, input string) {
		t.Helper()
		if _, err := s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,workflow_id,input,created_at)VALUES(?,?,?,?,?,?)`, id, a.OwnerID, a.ID, w.ID, input, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	insertWorkflowRun("graph-run", `{"organization":{"steps":[]}}`)
	if err = execRun(s, ctx, "graph-run"); err == nil || err.Error() != "workflow_graph_unsupported" {
		t.Fatal("graph workflow executed", err)
	}
	insertWorkflowRun("paused-run", "plain")
	if err = execRun(s, ctx, "paused-run"); err == nil || err.Error() != "workflow_disabled" {
		t.Fatal("disabled workflow executed", err)
	}

	// A run claiming a linear flow it does not belong to.
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,request_key,input,created_at)VALUES('forged',?,?,'linear:nope:0','x',?)`, a.OwnerID, a.ID, db.Now()); err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, ctx, "forged"); err == nil || err.Error() != "linear_context_invalid" {
		t.Fatal("forged linear run executed", err)
	}
	for _, id := range []string{disabledOwner, disabledAgent, "graph-run", "paused-run", "forged"} {
		if status, _ := runStatus(t, s, id); status != "failed" {
			t.Fatal("run not marked failed", id, status)
		}
	}

	// Missing auth service.
	noAuth := queue("no auth")
	s.d.Services = kernel.NewServiceRegistry()
	if err = execRun(s, ctx, noAuth); err == nil || err.Error() != "owner_unavailable" {
		t.Fatal("run executed without auth service", err)
	}
}

func TestExecuteReportsProviderAndToolFailures(t *testing.T) {
	var behaviour func(ctx context.Context) (string, error)
	s, ctx := setup(t, func(c context.Context, _ *kernel.Deps, _, _, _, _ string) (string, error) {
		return behaviour(c)
	})
	text, err := s.saveAgent(ctx, Agent{Name: "Text", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		provider func(context.Context) (string, error)
		code     string
	}{
		{"provider error", func(context.Context) (string, error) { return "", errors.New("upstream down") }, "provider_failed"},
		{"empty result", func(context.Context) (string, error) { return "   ", nil }, "invalid_provider_result"},
		{"tool error", func(context.Context) (string, error) { return "", toolError("custom_tool_code") }, "custom_tool_code"},
	}
	for _, c := range cases {
		behaviour = c.provider
		id, err := s.run(ctx, text.ID, c.name, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = execRun(s, ctx, id); err == nil || err.Error() != c.code {
			t.Fatal(c.name, err)
		}
		if status, code := runStatus(t, s, id); status != "failed" || code != c.code {
			t.Fatal(c.name, status, code)
		}
	}

	// Cancellation during the provider call leaves the run for retry.
	cancelCtx, cancel := context.WithCancel(ctx)
	behaviour = func(context.Context) (string, error) { cancel(); return "late", nil }
	id, err := s.run(ctx, text.ID, "cancelled", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, cancelCtx, id); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not surfaced", err)
	}
	if status, _ := runStatus(t, s, id); status != "running" {
		t.Fatal("cancelled run finalised", status)
	}

	// github_stars rejects malformed tool input with its own error code.
	stars, err := s.saveAgent(ctx, Agent{Name: "Stars", Enabled: true, ExecutionKind: "github_stars"})
	if err != nil {
		t.Fatal(err)
	}
	id, err = s.run(ctx, stars.ID, "not json", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, ctx, id); err == nil || err.Error() != "invalid_tool_input" {
		t.Fatal("tool input error code lost", err)
	}
	// content_draft without the content module fails closed.
	draft, err := s.saveAgent(ctx, Agent{Name: "Draft", Enabled: true, ExecutionKind: "content_draft"})
	if err != nil {
		t.Fatal(err)
	}
	id, err = s.run(ctx, draft.ID, `{"type":"post","title":"T","body":"B"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = execRun(s, ctx, id); err == nil || err.Error() != "content_service_unavailable" {
		t.Fatal("draft without content service", err)
	}
}

func TestExecuteSurfacesStorageFailures(t *testing.T) {
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return "result", nil
	})
	a, err := s.saveAgent(ctx, Agent{Name: "Store", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.run(ctx, a.ID, "artifact", "")
	if err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "no_artifacts", "INSERT", "workagents_artifacts", "")
	if err = execRun(s, ctx, artifact); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("artifact failure hidden", err)
	}
	if status, _ := runStatus(t, s, artifact); status == "done" {
		t.Fatal("run marked done without artifact")
	}
	starting, err := s.run(ctx, a.ID, "start", "")
	if err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "no_running", "UPDATE", "workagents_runs", "NEW.status='running'")
	if err = execRun(s, ctx, starting); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("start failure hidden", err)
	}
	abortOn(t, s, "no_failed", "UPDATE", "workagents_runs", "NEW.status='failed'")
	if err = s.fail(ctx, starting, "x"); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("fail() hid storage error", err)
	}
}

func TestSaveWorkflowValidationAndOwnership(t *testing.T) {
	s, ctx := setup(t, nil)
	if _, err := s.saveWorkflow(ctx, Workflow{AgentID: "missing", Name: "x", Input: "x", IntervalSeconds: 60}); !errors.Is(err, errNotFound) {
		t.Fatal("workflow for missing agent", err)
	}
	customer, u := customerCtx(t, s, "wf-customer@example.com")
	owned, err := s.saveAgent(ctx, Agent{Name: "Customer owned", OwnerID: u.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveWorkflow(customer, Workflow{AgentID: owned.ID, Name: "x", Input: "x", IntervalSeconds: 60}); !errors.Is(err, errUnavailable) {
		t.Fatal("customer managed schedule", err)
	}
	disabled, err := s.saveAgent(ctx, Agent{Name: "Off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveWorkflow(ctx, Workflow{AgentID: disabled.ID, Name: "x", Input: "x", IntervalSeconds: 60, Enabled: true}); !errors.Is(err, errUnavailable) {
		t.Fatal("enabled schedule for disabled agent", err)
	}
	for _, w := range []Workflow{
		{AgentID: owned.ID, Name: "x", Input: "x", IntervalSeconds: 59},
		{AgentID: owned.ID, Name: "x", Input: "x", IntervalSeconds: 31536001},
		{AgentID: owned.ID, Name: "x", Input: " ", IntervalSeconds: 60},
		{AgentID: owned.ID, Name: "", Input: "x", IntervalSeconds: 60},
	} {
		if _, err = s.saveWorkflow(ctx, w); !errors.Is(err, errInvalid) {
			t.Fatalf("invalid workflow %+v: %v", w, err)
		}
	}
	if _, err = s.saveWorkflow(ctx, Workflow{ID: "missing", AgentID: owned.ID, Name: "x", Input: "x", IntervalSeconds: 60}); !errors.Is(err, errNotFound) {
		t.Fatal("update of unknown workflow", err)
	}
	w, err := s.saveWorkflow(ctx, Workflow{AgentID: owned.ID, Name: "Keep", Input: "x", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	w.AgentID = disabled.ID
	if _, err = s.saveWorkflow(ctx, w); !errors.Is(err, errInvalid) {
		t.Fatal("workflow moved to another agent", err)
	}
}

func TestScheduleSurfacesStorageFailures(t *testing.T) {
	for _, c := range []struct{ name, table, event, when string }{
		{"enqueue", "jobs", "INSERT", ""},
		{"run insert", "workagents_runs", "INSERT", ""},
		{"advance", "workagents_workflows", "UPDATE", "NEW.next_run_at<>OLD.next_run_at"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, ctx := setup(t, nil)
			a, err := s.saveAgent(ctx, Agent{Name: "Sched", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.saveWorkflow(ctx, Workflow{AgentID: a.ID, Name: "Due", Input: "x", IntervalSeconds: 60, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.d.DB.Exec(`UPDATE workagents_workflows SET next_run_at='2000-01-01T00:00:00Z'`); err != nil {
				t.Fatal(err)
			}
			abortOn(t, s, "inject", c.event, c.table, c.when)
			if err = s.schedule(ctx); err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatal("schedule hid storage failure", err)
			}
			var n int
			if err = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs`).Scan(&n); err != nil || n != 0 {
				t.Fatal("partial schedule committed", n, err)
			}
		})
	}
	t.Run("query", func(t *testing.T) {
		s, ctx := setup(t, nil)
		if _, err := s.d.DB.Exec(`DROP TABLE workagents_workflows`); err != nil {
			t.Fatal(err)
		}
		if err := s.schedule(ctx); err == nil {
			t.Fatal("schedule query failure hidden")
		}
	})
}

func TestAuditTrailRecordsScheduleQueueAndFailures(t *testing.T) {
	s, ctx := setup(t, nil)
	if err := db.Migrate(ctx, s.d.DB, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	s.d.Audit = audit.New(s.d.DB, s.d.Log)
	a, err := s.saveAgent(ctx, Agent{Name: "Audited", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveWorkflow(ctx, Workflow{AgentID: a.ID, Name: "Due", Input: "x", IntervalSeconds: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.d.DB.Exec(`UPDATE workagents_workflows SET next_run_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err = s.schedule(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := s.runs(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	if err = execRun(s, ctx, runs[0].ID); err == nil || err.Error() != "ai_unconfigured" {
		t.Fatal(err)
	}
	for _, action := range []string{"workagent.profile.saved", "workagent.schedule.saved", "workagent.schedule.queued", "workagent.run.failed"} {
		var n int
		if err = s.d.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action=?`, action).Scan(&n); err != nil || n == 0 {
			t.Fatal("missing audit event", action, n, err)
		}
	}
}
