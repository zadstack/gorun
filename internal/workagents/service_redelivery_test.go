package workagents

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// A job redelivered while its run is already running (stale-heartbeat
// recovery) must not call the provider again.
func TestRedeliveredRunningRunIsNotExecutedAgain(t *testing.T) {
	calls := 0
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		calls++
		return "result", nil
	})
	a, e := s.saveAgent(ctx, Agent{Name: "Once", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.run(ctx, a.ID, "Do it once", "")
	if e != nil {
		t.Fatal(e)
	}
	// An earlier delivery claimed the run and is (or was) executing it.
	if _, e = s.d.DB.Exec(`UPDATE workagents_runs SET status='running' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id})
	if e = s.execute(ctx, payload); e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatalf("provider called %d times for a run already running", calls)
	}
	var status, code string
	if e = s.d.DB.QueryRow(`SELECT status,error_code FROM workagents_runs WHERE id=?`, id).Scan(&status, &code); e != nil {
		t.Fatal(e)
	}
	if status != "failed" || code != "worker_interrupted" {
		t.Fatalf("redelivered run = %s/%s, want failed/worker_interrupted", status, code)
	}

	// A queued run still executes exactly once.
	id2, e := s.run(ctx, a.ID, "Second", "")
	if e != nil {
		t.Fatal(e)
	}
	payload, _ = json.Marshal(map[string]string{"run_id": id2})
	if e = s.execute(ctx, payload); e != nil {
		t.Fatal(e)
	}
	if e = s.execute(ctx, payload); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}
