package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

func (h *handlers) managedToolApproval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if auth.ActorFromContext(r.Context()).Kind != "user" || !auth.Can(r.Context(), "content.edit") || !WorkspaceShowsAdvanced(r.Context(), h.d.Settings) {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	if r.Method == "POST" && !auth.RecentlyAuthenticated(r.Context(), auth.ReauthWindow) {
		h.aiJSON(w, 403, map[string]string{"error": "reauth_required", "reauth_url": "/admin/reauth?next=" + url.QueryEscape("/admin/canvases/"+b.ID)})
		return
	}
	id := chi.URLParam(r, "run")
	var raw, status, stateRaw string
	var revision int64
	if h.d.DB.QueryRowContext(r.Context(), `SELECT r.document,r.status,s.document,s.revision FROM admin_canvas_runs r JOIN admin_canvas_tool_states s ON s.run_id=r.id WHERE r.id=? AND r.board_id=? AND r.owner_id=? AND s.owner_id=r.owner_id`, id, b.ID, auth.UserFromContext(r.Context()).ID).Scan(&raw, &status, &stateRaw, &revision) != nil {
		http.NotFound(w, r)
		return
	}
	var p canvasPrepared
	var s managedState
	if json.Unmarshal([]byte(raw), &p) != nil || json.Unmarshal([]byte(stateRaw), &s) != nil || status != "waiting_tool" || s.Pending == nil || s.Pending.Approved || s.Pending.Attempted || !managedStateValid(s) {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	if !agentBundleOwnerCurrent(r.Context(), h.d.DB, p.OwnerID, p.KeyID) {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	expires, e := time.Parse(time.RFC3339Nano, s.Pending.ExpiresAt)
	if e != nil || !time.Now().Before(expires) {
		h.canvasOpsError(w, 409, "tool_review_expired")
		return
	}
	ctx, current, cfg, e := h.currentManagedPrepared(r.Context(), p)
	if e != nil {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	p = current
	tool, ok := findManagedTool(p, s.Pending.Call.Function.Name)
	if !ok {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	secrets, e := h.managedSecrets(ctx, p, cfg)
	if e != nil || managedContainsSecret(s.Pending.Call.Function.Arguments, secrets) {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	connection, e := h.loadConnection(ctx, tool.ConnectionID)
	if e != nil || connection.Version != tool.ConnectionVersion {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	endpoint, e := url.Parse(connection.Endpoint)
	if e != nil {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	if r.Method == "GET" {
		h.aiJSON(w, 200, map[string]any{"approval_id": s.Pending.ID, "revision": revision, "tool": tool.Name, "destination": connection.Name, "host": endpoint.Hostname(), "arguments": json.RawMessage(s.Pending.Call.Function.Arguments), "expires_at": s.Pending.ExpiresAt, "calls_used": s.Calls, "calls_max": 3})
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
		Revision   int64  `json:"revision"`
		Approve    bool   `json:"approve"`
	}
	if studioRead(w, r, &in) != nil || in.ApprovalID != s.Pending.ID || in.Revision != revision {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if e := managedAuthorityTx(ctx, tx, p); e != nil {
			return e
		}
		var storedRevision int64
		var storedStatus string
		if tx.QueryRowContext(ctx, `SELECT s.revision,r.status FROM admin_canvas_tool_states s JOIN admin_canvas_runs r ON r.id=s.run_id WHERE r.id=? AND r.owner_id=?`, id, p.OwnerID).Scan(&storedRevision, &storedStatus) != nil || storedRevision != revision || storedStatus != "waiting_tool" || !time.Now().Before(expires) {
			return errManagedTools
		}
		s.Pending.Approved = in.Approve
		next := "cancelled"
		if in.Approve {
			next = "queued"
		}
		result, e := tx.ExecContext(ctx, `UPDATE admin_canvas_tool_states SET document=?,revision=revision+1 WHERE run_id=? AND owner_id=? AND revision=?`, canvasEncode(s), id, p.OwnerID, revision)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return errManagedTools
		}
		result, e = tx.ExecContext(ctx, `UPDATE admin_canvas_runs SET status=?,finished_at=CASE WHEN ?='cancelled' THEN ? ELSE finished_at END WHERE id=? AND owner_id=? AND status='waiting_tool'`, next, next, db.Now(), id, p.OwnerID)
		if e != nil {
			return e
		}
		n, e = result.RowsAffected()
		if e != nil || n != 1 {
			return errManagedTools
		}
		if in.Approve {
			if _, e = h.d.Jobs.EnqueueTx(ctx, tx, "canvas.tools", map[string]string{"id": id}, jobs.Idempotency("canvas:tool:"+id+":"+s.Pending.ID)); e != nil {
				return e
			}
		}
		action := "canvas.tool.rejected"
		if in.Approve {
			action = "canvas.tool.approved"
		}
		h.d.Audit.RecordTx(ctx, tx, action, "canvas_run", id, map[string]any{"tool": tool.Alias, "step": s.Turns})
		return nil
	})
	if e != nil {
		h.canvasOpsError(w, 409, "tool_review_changed")
		return
	}
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}
