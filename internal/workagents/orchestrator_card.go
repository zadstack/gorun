package workagents

import (
	"context"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// The orchestrator sidebar card is owner-scoped counters only: it never
// exposes agent prompt text, run inputs or approval bodies.
type orchestratorCardData struct {
	Orchestrator bool   // the owner's seeded example orchestrator profile exists
	Status       string // "running" when a run is live, otherwise "idle"
	Pending      int    // pending approvals owned by the viewer
	DoneToday    int    // runs of this owner finished since the start of the UTC day
}

// orchestratorCard answers the htmx swap target for the sidebar contact card.
func (h *handlers) orchestratorCard(w http.ResponseWriter, r *http.Request) {
	if owner(r.Context()) == "" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "active account required", nil)
		return
	}
	if h.adminTpl == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := h.s.orchestratorCard(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "workagents_failed", "agent state unavailable", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.adminTpl.RenderPartial(w, r, "partials/orchestrator_card", data)
}

func (s *service) orchestratorCard(ctx context.Context) (orchestratorCardData, error) {
	out := orchestratorCardData{Status: "idle"}
	o := owner(ctx)
	var present int
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_agents WHERE owner_id=? AND source_id=?`, o, exampleOrchestratorSource+":"+o).Scan(&present); err != nil {
		return out, err
	}
	out.Orchestrator = present > 0
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_approvals WHERE owner_id=? AND status='pending'`, o).Scan(&out.Pending); err != nil {
		return out, err
	}
	var running int
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_runs WHERE owner_id=? AND status='running'`, o).Scan(&running); err != nil {
		return out, err
	}
	if running > 0 {
		out.Status = "running"
	}
	dayStart := db.FormatTime(s.now().UTC().Truncate(24 * time.Hour))
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_runs WHERE owner_id=? AND status='done' AND finished_at>=?`, o, dayStart).Scan(&out.DoneToday); err != nil {
		return out, err
	}
	if err := s.mcpPendingApprovals(ctx, o, &out.Pending); err != nil {
		return out, err
	}
	return out, nil
}

// mcpPendingApprovals adds pending agent tool approvals requested by this
// owner. The module may not import internal/mcp, so it reads the neighbor
// table with plain SQL exactly like internal/admin does, and skips entirely
// when the MCP addon never created its tables.
func (s *service) mcpPendingApprovals(ctx context.Context, o string, pending *int) error {
	var tables int
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='mcp_approvals'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil
	}
	var n int
	if err := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM mcp_approvals WHERE status='pending' AND expires_at>? AND requested_by=?`, db.Now(), o).Scan(&n); err != nil {
		return err
	}
	*pending += n
	return nil
}
