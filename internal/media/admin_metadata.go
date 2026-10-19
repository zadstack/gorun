package media

import (
	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/httpx"
	"net/http"
)

// Explicit projection: storage keys, original remote source URLs and credentials
// never enter the library details response. The existing media.upload gate applies.
func (h *adminHandlers) metadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m, e := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	item := toGridItem(h.svc, *m)
	httpx.JSON(w, 200, map[string]any{"id": m.ID, "title": m.Title, "filename": m.Filename, "alt": m.Alt, "caption": m.Caption, "mime": m.Mime, "size": item.Size, "dimensions": item.Dims}, nil)
}
