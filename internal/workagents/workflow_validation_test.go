package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/kernel"
)

func TestImportedApprovalGraphCannotSkipItsGate(t *testing.T) {
	calls := 0
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		calls++
		return "must not execute", nil
	})
	a, err := s.saveAgent(ctx, Agent{Name: "Researcher", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	input := `{"source_config":{"organization":{"nodes":[{"type":"agent"},{"type":"approval"}],"requires_human_approval":true}}}`
	w := Workflow{AgentID: a.ID, Name: "Imported", Input: input, Enabled: true, IntervalSeconds: 60}
	if _, err = s.saveWorkflow(ctx, w); !errors.Is(err, errWorkflowGraph) {
		t.Fatalf("unsafe graph activation: %v", err)
	}
	w.Enabled = false
	w, err = s.saveWorkflow(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.workflows(ctx)
	if err != nil || len(listed) != 1 || listed[0].BlockedReason != "workflow_graph_unsupported" {
		t.Fatalf("missing readiness reason: %#v %v", listed, err)
	}
	// Legacy enabled rows must be blocked even when they bypassed the API guard.
	if _, err = s.d.DB.Exec(`UPDATE workagents_workflows SET enabled=1,next_run_at='' WHERE id=?`, w.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.schedule(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := s.runs(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatalf("unsafe scheduler queue: %v %v", runs, err)
	}
	// Jobs already queued before the guard must not reach providers either.
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,workflow_id,input,created_at) VALUES('legacy',?,?,?,?,?)`, a.OwnerID, a.ID, w.ID, input, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err = s.execute(ctx, json.RawMessage(`{"run_id":"legacy"}`)); err == nil || err.Error() != "workflow_graph_unsupported" {
		t.Fatalf("unsafe legacy execution: %v", err)
	}
	if calls != 0 {
		t.Fatalf("provider called %d times", calls)
	}
}

func TestWorkflowGraphDetectionLeavesNativeInputsIntact(t *testing.T) {
	for _, input := range []string{"Write a draft", `{"repository":"octocat/Hello-World"}`, `{"username":"octocat"}`, `{"title":"Example","body":"Text"}`} {
		if workflowBlockedReason(input) != "" {
			t.Fatalf("native input blocked: %s", input)
		}
	}
	if workflowBlockedReason(`{"organization":{"nodes":[]}}`) == "" {
		t.Fatal("direct graph accepted")
	}
}

func TestWorkflowGraphFailureIsActionableConflict(t *testing.T) {
	r := httptest.NewRecorder()
	(&handlers{}).failure(r, errWorkflowGraph)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "workflow_graph_unsupported") {
		t.Fatalf("unexpected API error: %d %s", r.Code, r.Body.String())
	}
}
