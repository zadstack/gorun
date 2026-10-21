package workagents

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestSeedExampleOrchestratorAgents(t *testing.T) {
	s, adminCtx, app := bootWorkspace(t)
	ctx := context.Background()
	if err := seedExampleOrchestratorAgents(ctx, app.Deps, func() string { return "2026-10-21T00:00:00Z" }); err != nil {
		t.Fatal(err)
	}
	owner := auth.UserFromContext(adminCtx).ID
	var id string
	if err := app.Deps.DB.QueryRowContext(ctx, `SELECT id FROM workagents_agents WHERE source_id=?`, exampleOrchestratorSource+":"+owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	a, err := s.agent(adminCtx, id)
	if err != nil {
		t.Fatal(err)
	}
	if a.Enabled || a.Name != "Example: Orchestrator" || a.ExecutionKind != "text" {
		t.Fatalf("agent = %+v", a)
	}
	if err := seedExampleOrchestratorAgents(ctx, app.Deps, func() string { return "2026-10-21T00:00:00Z" }); err != nil {
		t.Fatal(err)
	}
}
