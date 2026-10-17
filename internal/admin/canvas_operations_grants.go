package admin

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"net/http"
	"slices"
	"time"
)

type canvasGrant struct {
	AgentID string   `json:"agent_id"`
	Title   string   `json:"title"`
	Read    bool     `json:"read"`
	Append  bool     `json:"append"`
	Run     bool     `json:"run"`
	NodeIDs []string `json:"node_ids"`
}

func (h *handlers) canvasAgentKey(ctx context.Context, id string) (*auth.APIKey, error) {
	keys, e := h.keys.List(ctx)
	if e != nil {
		return nil, e
	}
	u := auth.UserFromContext(ctx)
	for _, k := range keys {
		if k.ID == id && u != nil && k.UserID == u.ID && k.Kind == auth.KeyKindAgent && k.RevokedAt == "" && (k.ExpiresAt == "" || k.ExpiresAt > time.Now().UTC().Format(time.RFC3339)) {
			return &k, nil
		}
	}
	return nil, errCanvasConsent
}
func (h *handlers) canvasGrantAllows(ctx context.Context, board, key, action string, nodes []string) bool {
	k, e := h.canvasAgentKey(ctx, key)
	if e != nil {
		return false
	}
	scope := map[string]string{"read": "canvas.read", "append": "canvas.append", "run": "canvas.run"}[action]
	if !slices.Contains(k.Scopes, "*") && !slices.Contains(k.Scopes, scope) {
		return false
	}
	var g canvasGrant
	var raw string
	if h.d.DB.QueryRowContext(ctx, `SELECT can_read,can_append,can_run,node_ids FROM admin_canvas_grants WHERE board_id=? AND key_id=?`, board, key).Scan(&g.Read, &g.Append, &g.Run, &raw) != nil {
		return false
	}
	if json.Unmarshal([]byte(raw), &g.NodeIDs) != nil {
		return false
	}
	if action == "read" && !g.Read || action == "append" && !g.Append || action == "run" && !g.Run {
		return false
	}
	for _, id := range nodes {
		if !slices.Contains(g.NodeIDs, id) {
			return false
		}
	}
	return true
}
func (h *handlers) canvasGrants(ctx context.Context, id string) ([]canvasGrant, error) {
	out := []canvasGrant{}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT key_id,can_read,can_append,can_run,node_ids FROM admin_canvas_grants WHERE board_id=?`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var g canvasGrant
		var raw string
		if e = rows.Scan(&g.AgentID, &g.Read, &g.Append, &g.Run, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &g.NodeIDs); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (h *handlers) canvasGrantHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if auth.ActorFromContext(r.Context()).Kind != "user" || !auth.Can(r.Context(), PermAPIKeys) {
		h.aiJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	if r.Method == "GET" {
		grants, e := h.canvasGrants(r.Context(), b.ID)
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		h.aiJSON(w, 200, map[string]any{"grants": grants})
		return
	}
	var g canvasGrant
	if studioRead(w, r, &g) != nil || len(g.NodeIDs) > 100 {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	if _, e = h.canvasAgentKey(r.Context(), g.AgentID); e != nil {
		h.canvasOpsError(w, 422, "invalid_key")
		return
	}
	seen := map[string]bool{}
	for _, c := range b.Cards {
		if slices.Contains([]string{"note", "prompt", "content", "source", "agent", "provider", "schedule"}, c.Kind) {
			seen[c.ID] = true
		}
	}
	for _, id := range g.NodeIDs {
		if !seen[id] {
			h.canvasOpsError(w, 422, "invalid_node")
			return
		}
	}
	_, e = h.d.DB.ExecContext(r.Context(), `INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,?,?,?,?) ON CONFLICT(board_id,key_id) DO UPDATE SET can_read=excluded.can_read,can_append=excluded.can_append,can_run=excluded.can_run,node_ids=excluded.node_ids`, b.ID, g.AgentID, g.Read, g.Append, g.Run, canvasEncode(g.NodeIDs))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.grant", "canvas", b.ID, map[string]any{"key_id": g.AgentID, "read": g.Read, "append": g.Append, "run": g.Run})
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}
