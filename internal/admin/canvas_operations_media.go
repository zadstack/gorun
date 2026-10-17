package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/jobs"
	_ "golang.org/x/image/webp"
)

// Canvas files remain outside the public media store; only this authenticated route serves them.
func (h *handlers) canvasImportPrivate(ctx context.Context, p *canvasPrepared, out studioOutput) error {
	data := out.Bytes
	var e error
	if len(data) == 0 {
		data, e = studioFetch(ctx, studioSafeClient(), out.URL)
		if e != nil {
			return e
		}
	}
	if len(data) == 0 || len(data) > 64<<20 {
		return errStudio
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
			return errStudio
		}
	case "video/mp4":
	default:
		return errStudio
	}
	if p.Candidate.Input.Kind == "image" && !strings.HasPrefix(mime, "image/") || p.Candidate.Input.Kind == "video" && mime != "video/mp4" {
		return errStudio
	}
	dir := filepath.Join(h.d.Config.DataDir, "canvas-results")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errStudio
	}
	id, e := canvasUUID()
	if e != nil {
		return e
	}
	name := id + ".bin"
	file, e := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = file.Write(data)
	closeErr := file.Close()
	if e != nil || closeErr != nil {
		_ = os.Remove(filepath.Join(dir, name))
		return errStudio
	}
	p.MediaMIME, p.MediaFile, p.MediaSize = mime, name, int64(len(data))
	return nil
}
func (h *handlers) canvasMediaHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	// Media access is session-only. A board/key text grant does not grant its private files.
	if auth.ActorFromContext(r.Context()).Kind != "user" {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	var raw string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT document FROM admin_canvas_runs WHERE id=? AND board_id=? AND owner_id=? AND status='succeeded'`, chi.URLParam(r, "run"), b.ID, auth.UserFromContext(r.Context()).ID).Scan(&raw)
	var p canvasPrepared
	if e != nil || json.Unmarshal([]byte(raw), &p) != nil || !strings.HasSuffix(p.MediaFile, ".bin") || !canvasCardID.MatchString(strings.TrimSuffix(p.MediaFile, ".bin")) {
		h.canvasOpsError(w, 404, "not_found")
		return
	}
	file, e := os.Open(filepath.Join(h.d.Config.DataDir, "canvas-results", p.MediaFile))
	if e != nil {
		h.canvasOpsError(w, 404, "not_found")
		return
	}
	defer file.Close()
	st, e := file.Stat()
	if e != nil || !st.Mode().IsRegular() {
		h.canvasOpsError(w, 404, "not_found")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", p.MediaMIME)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, "canvas-result", st.ModTime(), file)
}
func (h *handlers) canvasCheckHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	id := chi.URLParam(r, "run")
	var raw, status string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT document,status FROM admin_canvas_runs WHERE id=? AND board_id=? AND owner_id=?`, id, b.ID, auth.UserFromContext(r.Context()).ID).Scan(&raw, &status)
	var p canvasPrepared
	if e != nil || json.Unmarshal([]byte(raw), &p) != nil || status != "waiting" || p.Candidate.Binding == nil || p.Candidate.Binding.StatusTool == "" {
		h.canvasOpsError(w, 409, "conflict")
		return
	}
	current, e := h.canvasPrepare(r.Context(), b, p.NodeID)
	if e != nil || current.ReviewHash != p.ReviewHash || !h.canvasScheduleActive(r.Context(), p) {
		h.canvasOpsError(w, 409, "consent_changed")
		return
	}
	if !h.aiLimit.allow("canvas-poll:" + auth.UserFromContext(r.Context()).ID) {
		h.canvasOpsError(w, 429, "rate_limited")
		return
	}
	tx, e := h.d.DB.BeginTx(r.Context(), nil)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(r.Context(), `UPDATE admin_canvas_runs SET status='queued' WHERE id=? AND status='waiting'`, id)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		h.canvasOpsError(w, 409, "conflict")
		return
	}
	_, e = h.d.Jobs.EnqueueTx(r.Context(), tx, "canvas.generate", map[string]any{"id": id, "poll": true}, jobs.Idempotency("canvas:poll:"+id+":"+time.Now().UTC().Format(time.RFC3339Nano)))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if e = tx.Commit(); e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.aiJSON(w, 202, map[string]bool{"ok": true})
}
func (h *handlers) canvasResults(ctx context.Context, board string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT id,document FROM admin_canvas_runs WHERE board_id=? AND status='succeeded' AND result_card_id!=''`, board)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			return nil, e
		}
		var p canvasPrepared
		if json.Unmarshal([]byte(raw), &p) != nil {
			return nil, errCanvasInvalid
		}
		if p.MediaFile != "" {
			kind := "image"
			if p.MediaMIME == "video/mp4" {
				kind = "video"
			}
			out[id] = map[string]string{"kind": kind, "url": "/admin/canvases/" + board + "/operations/runs/" + id + "/media", "status": "succeeded"}
		}
	}
	return out, rows.Err()
}
