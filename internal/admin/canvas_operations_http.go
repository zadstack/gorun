package admin

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"net/http"
	"time"
)

func (h *handlers) canvasOperationRoutes() {
	r := h.d.Router.Admin
	r.Get("/canvases/{id}/operations/options", h.canvasOptions)
	r.Post("/canvases/{id}/operations/preview", h.canvasReviewHTTP)
	r.Get("/canvases/{id}/operations/runs", h.canvasRunsHTTP)
	r.Post("/canvases/{id}/operations/runs", h.canvasRunsHTTP)
	r.Post("/canvases/{id}/operations/runs/{run}/check", h.canvasCheckHTTP)
	r.Post("/canvases/{id}/operations/runs/{run}/cancel", h.canvasCancelHTTP)
	r.Get("/canvases/{id}/operations/runs/{run}/media", h.canvasMediaHTTP)
	r.Get("/canvases/{id}/operations/grants", h.canvasGrantHTTP)
	r.Post("/canvases/{id}/operations/grants", h.canvasGrantHTTP)
	r.Post("/canvases/{id}/operations/schedules/{node}", h.canvasScheduleHTTP)
}
func (h *handlers) canvasOpsError(w http.ResponseWriter, status int, code string) {
	h.aiJSON(w, status, map[string]string{"error": code})
}
func (h *handlers) canvasOptions(w http.ResponseWriter, r *http.Request) {
	if _, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id")); e != nil {
		h.canvasError(w, r, e)
		return
	}
	types := []map[string]string{}
	providers := []map[string]any{}
	agents := []map[string]string{}
	if cs, ok := h.contentService(); ok && auth.Can(r.Context(), "content.view") {
		ts, e := cs.Types(r.Context())
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		for _, t := range ts {
			if t.Public {
				types = append(types, map[string]string{"id": t.Slug, "title": t.Slug})
			}
		}
	}
	if cfg, e := h.loadAIConfig(r.Context()); e == nil {
		providers = append(providers, map[string]any{"id": "native-text", "title": cfg.Model, "endpoint": cfg.BaseURL, "configured": true, "kind": "text"})
	}
	if cfg, e := h.studioConfig(r.Context()); e == nil {
		for _, b := range cfg.Bindings {
			if studioKind(b.Kind) {
				c, e := h.loadConnection(r.Context(), b.ConnectionID)
				ready := e == nil && c.Version == b.ConnectionVersion && c.Endpoint == b.Endpoint
				providers = append(providers, map[string]any{"id": b.ID, "title": b.Name, "endpoint": b.Endpoint, "configured": ready, "kind": b.Kind})
			}
		}
	}
	if auth.Can(r.Context(), PermAPIKeys) {
		keys, e := h.keys.List(r.Context())
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		for _, k := range keys {
			if _, e = h.canvasAgentKey(r.Context(), k.ID); e == nil {
				agents = append(agents, map[string]string{"id": k.ID, "title": k.Name})
			}
		}
	}
	presets := []map[string]string{}
	if sc, e := h.studioConfig(r.Context()); e == nil {
		for _, p := range sc.Presets {
			presets = append(presets, map[string]string{"id": p.ID, "title": p.Name, "kind": p.Kind})
		}
		if cfg, e := h.loadAIConfig(r.Context()); e == nil && cfg.Provider == AIProviderOpenAI && sc.ImageModel != "" {
			providers = append(providers, map[string]any{"id": "native-image", "title": sc.ImageModel, "endpoint": cfg.BaseURL, "kind": "image", "configured": true})
		}
	}
	h.aiJSON(w, 200, map[string]any{"presets": presets, "types": types, "providers": providers, "agents": agents, "can_share": auth.Can(r.Context(), PermAPIKeys)})
}
func (h *handlers) canvasReviewHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	var in struct {
		Version int64  `json:"version"`
		NodeID  string `json:"node_id"`
	}
	if studioRead(w, r, &in) != nil {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	if in.Version != b.Version {
		h.canvasOpsError(w, 409, "conflict")
		return
	}
	p, e := h.canvasPrepare(r.Context(), b, in.NodeID)
	if e != nil {
		h.canvasOpsError(w, 422, "unavailable")
		return
	}
	token, expires := db.NewID(), time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339)
	result, e := h.d.DB.ExecContext(r.Context(), `INSERT INTO admin_canvas_reviews(id,board_id,owner_id,node_id,document,expires_at) SELECT ?,?,?,?,?,? WHERE (SELECT count(*) FROM admin_canvas_reviews WHERE board_id=? AND expires_at>?)<100`, token, b.ID, p.OwnerID, p.NodeID, canvasEncode(p), expires, b.ID, db.Now())
	if e == nil {
		n, err := result.RowsAffected()
		if err != nil {
			e = err
		} else if n != 1 {
			h.canvasOpsError(w, 429, "review_limit")
			return
		}
	}
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.aiJSON(w, 200, map[string]any{"review_token": token, "expires_at": expires, "board_version": b.Version, "node_id": p.NodeID, "provider": p.Provider, "prompt": p.Prompt, "context": p.Context, "bytes": len(p.Context), "items": []any{}})
}
func (h *handlers) canvasRunsHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if r.Method == "GET" {
		runs, e := h.canvasRuns(r.Context(), b.ID)
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		h.aiJSON(w, 200, map[string]any{"runs": runs})
		return
	}
	var in struct {
		RequestID   string `json:"request_id"`
		ReviewToken string `json:"review_token"`
	}
	if studioRead(w, r, &in) != nil || !canvasRequestID(in.RequestID) {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	var raw string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT document FROM admin_canvas_reviews WHERE id=? AND board_id=? AND owner_id=? AND expires_at>?`, in.ReviewToken, b.ID, auth.UserFromContext(r.Context()).ID, db.Now()).Scan(&raw)
	var old canvasPrepared
	if e != nil || json.Unmarshal([]byte(raw), &old) != nil {
		h.canvasOpsError(w, 409, "review_expired")
		return
	}
	var prior int
	if h.d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM admin_canvas_runs WHERE board_id=? AND request_id=?`, b.ID, in.RequestID).Scan(&prior) == nil && prior > 0 {
		run, e := h.canvasEnqueue(r.Context(), old, in.RequestID)
		if e != nil {
			h.canvasOpsError(w, 409, "conflict")
			return
		}
		h.aiJSON(w, 202, map[string]any{"run": run})
		return
	}
	current, e := h.canvasPrepare(r.Context(), b, old.NodeID)
	if e != nil || current.Version != old.Version || current.ReviewHash != old.ReviewHash {
		h.canvasOpsError(w, 409, "consent_changed")
		return
	}
	run, e := h.canvasEnqueue(r.Context(), current, in.RequestID)
	if e != nil {
		h.canvasOpsError(w, 409, "conflict")
		return
	}
	h.aiJSON(w, 202, map[string]any{"run": run})
}
func canvasRequestID(s string) bool {
	return canvasCardID.MatchString(s) || connectionIDPattern.MatchString(s)
}
