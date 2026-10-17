package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

var (
	errCanvasDeleteBusy      = errors.New("canvas: active runs prevent deletion")
	errCanvasDeleteMedia     = errors.New("canvas: private media requires cleanup")
	errCanvasDeleteForbidden = errors.New("canvas: deletion forbidden")
)

func (h *handlers) canvasDelete(w http.ResponseWriter, r *http.Request) {
	if auth.ActorFromContext(r.Context()).Kind != "user" || !auth.Can(r.Context(), "content.edit") {
		h.aiJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var in struct {
		Version int64 `json:"version"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || in.Version < 1 {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	e := h.deleteCanvas(r.Context(), chi.URLParam(r, "id"), in.Version)
	switch {
	case e == nil:
		h.aiJSON(w, 200, map[string]bool{"ok": true})
	case errors.Is(e, errCanvasDeleteForbidden):
		h.aiJSON(w, 403, map[string]string{"error": "forbidden"})
	case errors.Is(e, errCanvasConsent):
		h.aiJSON(w, 409, map[string]string{"error": "conflict"})
	case errors.Is(e, errCanvasDeleteBusy):
		h.aiJSON(w, 409, map[string]string{"error": "busy"})
	case errors.Is(e, errCanvasDeleteMedia):
		h.aiJSON(w, 409, map[string]string{"error": "media"})
	default:
		h.canvasError(w, r, e)
	}
}

// Text-only deletion deliberately refuses private artifacts and active work.
func (h *handlers) deleteCanvas(ctx context.Context, id string, version int64) error {
	u := auth.UserFromContext(ctx)
	if u == nil || auth.ActorFromContext(ctx).Kind != "user" || !auth.Can(ctx, "content.edit") {
		return errCanvasDeleteForbidden
	}
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		var current int64
		if e := tx.QueryRowContext(ctx, `SELECT version FROM admin_canvases WHERE id=? AND owner_id=?`, id, u.ID).Scan(&current); e != nil {
			return e
		}
		if current != version {
			return errCanvasConsent
		}
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE u.id=? AND u.status='active' AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN ('*','content.edit'))`, u.ID).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return errCanvasDeleteForbidden
		}
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_runs WHERE board_id=? AND status NOT IN ('succeeded','failed','uncertain','cancelled','reviewed','requested')`, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errCanvasDeleteBusy
		}
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_runs WHERE board_id=? AND (coalesce(json_extract(document,'$.media_file'),'')!='' OR coalesce(json_extract(document,'$.media_mime'),'')!='' OR coalesce(json_extract(document,'$.media_size'),0)!=0)`, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errCanvasDeleteMedia
		}
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvases b,json_each(b.document_json) c WHERE b.id=? AND json_extract(c.value,'$.kind')='result'`, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errCanvasDeleteMedia
		}
		result, e := tx.ExecContext(ctx, `DELETE FROM admin_canvases WHERE id=? AND owner_id=? AND version=?`, id, u.ID, version)
		if e != nil {
			return e
		}
		n64, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n64 != 1 {
			return errCanvasConsent
		}
		h.d.Audit.RecordTx(ctx, tx, "canvas.delete", "canvas", id, map[string]int64{"version": version})
		return nil
	})
}
