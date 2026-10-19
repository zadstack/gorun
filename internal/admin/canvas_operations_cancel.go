package admin

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// Cancellation stops local dispatch/acceptance; it does not cancel external work.
func (h *handlers) canvasCancelHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	id, owner := chi.URLParam(r, "run"), auth.UserFromContext(r.Context()).ID
	e = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		var status string
		if e := tx.QueryRowContext(r.Context(), `SELECT status FROM admin_canvas_runs WHERE id=? AND board_id=? AND owner_id=?`, id, b.ID, owner).Scan(&status); e != nil {
			return e
		}
		if status == "cancelled" {
			return nil
		}
		switch status {
		case "requested", "queued", "waiting_agent", "waiting", "waiting_tool":
		default:
			return errCanvasConsent
		}
		result, e := tx.ExecContext(r.Context(), `UPDATE admin_canvas_runs SET status='cancelled',finished_at=? WHERE id=? AND board_id=? AND owner_id=? AND status=?`, db.Now(), id, b.ID, owner, status)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errCanvasConsent
		}
		return nil
	})
	if e == errCanvasConsent {
		h.canvasOpsError(w, 409, "conflict")
		return
	}
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}
