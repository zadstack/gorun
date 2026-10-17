package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func (h *handlers) canvasRoutes() {
	h.canvasOperationRoutes()
	r := h.d.Router.Admin
	r.Get("/canvases", h.canvasList)
	r.Post("/canvases", h.canvasNew)
	r.Get("/canvases/{id}", h.canvasEditor)
	r.Delete("/canvases/{id}", h.canvasDelete)
	r.Get("/canvases/{id}/references", h.canvasReferences)
	r.Get("/canvases/{id}/document", h.canvasDocument)
	r.Post("/canvases/{id}/document", h.canvasSave)
	r.Get("/canvases/{id}/widgets/{widgetID}", h.canvasWidget)
}
func (h *handlers) canvasList(w http.ResponseWriter, r *http.Request) {
	h.canvasListStatus(w, r, 200, "")
}
func (h *handlers) canvasListStatus(w http.ResponseWriter, r *http.Request, status int, message string) {
	boards, e := h.canvasBoards(r.Context())
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/canvases/list.html", struct {
		Boards []canvasBoard
		Error  string
	}{boards, h.tr(r, message)})
}
func (h *handlers) canvasNew(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.canvasListStatus(w, r, 422, "admin.canvas.invalid")
		return
	}
	preset := r.PostForm.Get("preset")
	if preset != "" && !slices.Contains([]string{"ask-content", "prepublish-review", "social-drafts", "periodic-brief", "evidence-worksheet", "story-video"}, preset) {
		h.canvasListStatus(w, r, 422, "admin.canvas.invalid")
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if !canvasText(title, 120) || title == "" {
		h.canvasListStatus(w, r, 422, "admin.canvas.invalid")
		return
	}
	id, e := h.canvasCreate(r.Context(), title)
	if errors.Is(e, errCanvasLimit) {
		h.canvasListStatus(w, r, 422, "admin.canvas.board_limit")
		return
	}
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.create", "canvas", id, nil)
	suffix := ""
	if preset != "" {
		suffix = "?preset=" + preset
	}
	http.Redirect(w, r, "/admin/canvases/"+id+suffix, 303)
}
func (h *handlers) canvasEditor(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.tpl.RenderWith(w, r, 200, "admin", "pages/canvases/editor.html", struct {
		Board   *canvasBoard
		Widgets []canvasWidgetOption
	}{b, h.canvasWidgetOptions(r)})
}
func (h *handlers) canvasError(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, sql.ErrNoRows) {
		h.aiJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	h.d.Log.Error("admin: canvas operation failed", "error", e)
	h.aiJSON(w, 500, map[string]string{"error": "unavailable"})
}
func (h *handlers) canvasJSON(w http.ResponseWriter, r *http.Request, b *canvasBoard) {
	refs := map[string]canvasReference{}
	for _, c := range b.Cards {
		if c.Kind == "content" {
			if ref, ok := h.canvasReference(r.Context(), c.ContentID); ok {
				refs[c.ContentID] = ref
			}
		}
	}
	results, err := h.canvasResults(r.Context(), b.ID)
	if err != nil {
		h.canvasError(w, r, err)
		return
	}
	provenance, schedules, err := h.canvasMetadata(r.Context(), b.ID)
	if err != nil {
		h.canvasError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.aiJSON(w, 200, struct {
		*canvasBoard
		References map[string]canvasReference   `json:"references"`
		Provenance map[string]map[string]string `json:"provenance"`
		Results    map[string]map[string]string `json:"results"`
		Schedules  map[string]canvasSchedule    `json:"schedules"`
	}{b, refs, provenance, results, schedules})
}
func (h *handlers) canvasDocument(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.canvasJSON(w, r, b)
}
func (h *handlers) canvasSave(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	var in struct {
		Version int64         `json:"version"`
		Title   string        `json:"title"`
		Cards   []canvasCard  `json:"cards"`
		Edges   *[]canvasEdge `json:"edges"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e = dec.Decode(&in); e != nil || dec.Decode(new(any)) != io.EOF || in.Version < 1 {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	if in.Version != b.Version {
		h.aiJSON(w, 409, map[string]string{"error": "conflict"})
		return
	}
	edges := b.Edges
	if in.Edges != nil {
		edges = *in.Edges
	}
	if validateCanvasEdges(in.Cards, edges) != nil {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	for _, c := range in.Cards {
		if c.Kind != "result" {
			continue
		}
		found := false
		for _, old := range b.Cards {
			if old.ID == c.ID && old.Kind == "result" && old.RunID == c.RunID && old.Title == c.Title && old.Body == c.Body {
				found = true
			}
		}
		if !found {
			h.aiJSON(w, 422, map[string]string{"error": "invalid"})
			return
		}
	}
	in.Title = strings.TrimSpace(in.Title)
	if h.validateCanvas(r.Context(), in.Title, in.Cards) != nil {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	if in.Cards == nil {
		in.Cards = []canvasCard{}
	}
	raw, e := json.Marshal(in.Cards)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	edgeRaw, _ := json.Marshal(edges)
	now := db.Now()
	result, e := h.d.DB.ExecContext(r.Context(), `UPDATE admin_canvases SET title=?,document_json=?,edges_json=?,version=version+1,updated_at=? WHERE id=? AND owner_id=? AND version=?`, in.Title, string(raw), string(edgeRaw), now, b.ID, auth.UserFromContext(r.Context()).ID, in.Version)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	n, e := result.RowsAffected()
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if n != 1 {
		h.aiJSON(w, 409, map[string]string{"error": "conflict"})
		return
	}
	b.Edges = edges
	b.Title, b.Cards, b.Version, b.UpdatedAt = in.Title, in.Cards, in.Version+1, now
	if e := h.canvasInvalidateSchedules(r.Context(), b); e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.d.Audit.Record(r.Context(), "canvas.save", "canvas", b.ID, map[string]any{"version": b.Version, "cards": len(b.Cards)})
	h.canvasJSON(w, r, b)
}
