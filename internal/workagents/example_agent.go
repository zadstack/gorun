package workagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const exampleOrchestratorSource = "example-orchestrator"

var exampleOrchestratorPrompt = `You are an orchestrator for a solo operator using GoRun.
Break the supplied goal into ordered private draft steps. For each step, name the matching role template (researcher, bilingual-editor, seo-reviewer, support-knowledge, operations-reader, campaign-planner), list inputs, expected private output, and the human approval needed next.
Never claim a step ran, a tool executed, or that payment, publishing, messaging or SSH occurred. Never mutate canvas state or customer accounts.`

// seedExampleOrchestratorAgents ensures each active admin has an inactive
// example orchestrator profile (idempotent per owner).
func seedExampleOrchestratorAgents(ctx context.Context, d *kernel.Deps, now func() string) error {
	rows, err := d.DB.QueryContext(ctx, `SELECT id FROM users WHERE role=? AND status=?`, auth.RoleAdmin, auth.StatusActive)
	if err != nil {
		return err
	}
	defer rows.Close()
	var admins []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		admins = append(admins, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tools, _ := json.Marshal([]string{})
	for _, ownerID := range admins {
		sourceID := exampleOrchestratorSource + ":" + ownerID
		var existing string
		err := d.DB.QueryRowContext(ctx, `SELECT id FROM workagents_agents WHERE source_id=?`, sourceID).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = d.DB.ExecContext(ctx, `INSERT INTO workagents_agents(id,owner_id,source_id,name,description,system_prompt,connection_id,model,enabled,unsupported_tools,created_at,updated_at,execution_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			db.NewID(), ownerID, sourceID,
			"Example: Orchestrator",
			"Inactive learning profile. Plans multi-step handoffs between role templates; enable only after you configure a text provider.",
			exampleOrchestratorPrompt, "", "", 0, string(tools), now(), now(), "text")
		if err != nil {
			return err
		}
	}
	return nil
}
