package admin

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

func (h *handlers) canvasReferences(w http.ResponseWriter, r *http.Request) {
	if _, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id")); e != nil {
		h.canvasError(w, r, e)
		return
	}
	type option struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Href  string `json:"href"`
		Type  string `json:"type"`
	}
	out := []option{}
	w.Header().Set("Cache-Control", "private, no-store")
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if !canvasText(query, 200) {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	cs, ok := h.contentService()
	if ok && auth.Can(r.Context(), "content.view") {
		q := content.Query{Search: query, PerPage: 20, PublicOnly: true, Order: []content.Order{{By: "updated_at", Dir: "desc"}}}
		u := auth.UserFromContext(r.Context())
		if u.Role == "author" || u.Role == "contributor" {
			q.AuthorID = u.ID
		}
		result, e := cs.Query(r.Context(), q)
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		for _, it := range result.Items {
			if ref, ok := h.canvasReference(r.Context(), it.ID); ok {
				out = append(out, option{it.ID, ref.Title, ref.Href, it.Type})
			}
		}
	}
	h.aiJSON(w, 200, map[string]any{"items": out})
}
