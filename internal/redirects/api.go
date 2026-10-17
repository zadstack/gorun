package redirects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// SaveRequest is the body of POST /v1/redirects (create when ID is empty).
type SaveRequest struct {
	ID       string `json:"id"`
	FromPath string `json:"from_path"`
	ToURL    string `json:"to_url"`
	Status   int    `json:"status" doc:"301 (default), 302, 307 or 308"`
	Enabled  *bool  `json:"enabled,omitempty" doc:"default true"`
}

// DeleteResponse is returned by DELETE /v1/redirects/{id}.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func registerAPI(d *kernel.Deps, svc *service) {
	a := &apiHandlers{svc: svc}
	tags, scopes := []string{"redirects"}, []string{Perm}
	d.API.Add(kernel.APIOperation{Method: http.MethodGet, Path: "/v1/redirects", Summary: "List redirects",
		Tags: tags, Scopes: scopes, Response: []Redirect{}, Handler: a.list, Params: []kernel.APIParam{
			{Name: "search", In: "query", Type: "string", Description: "matches from_path and to_url"},
			{Name: "page", In: "query", Type: "integer"}, {Name: "per_page", In: "query", Type: "integer"},
			{Name: "sort", In: "query", Type: "string", Description: "created_at | from_path | hits | status"},
			{Name: "order", In: "query", Type: "string", Description: "asc | desc"},
		}})
	d.API.Add(kernel.APIOperation{Method: http.MethodPost, Path: "/v1/redirects", Summary: "Create or update a redirect",
		Tags: tags, Scopes: scopes, Request: SaveRequest{}, Response: Redirect{}, Handler: a.save})
	d.API.Add(kernel.APIOperation{Method: http.MethodDelete, Path: "/v1/redirects/{id}", Summary: "Delete a redirect",
		Tags: tags, Scopes: scopes, Response: DeleteResponse{}, Handler: a.remove,
		Params: []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}})
}

type apiHandlers struct{ svc *service }

func allowed(w http.ResponseWriter, r *http.Request) bool {
	if !auth.Can(r.Context(), Perm) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+Perm, nil)
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid redirect", ve.Fields)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "redirect not found", nil)
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func (a *apiHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	page, perPage = normalizePage(page, perPage)
	items, total, err := a.svc.List(r.Context(), ListQuery{Search: q.Get("search"), Page: page, PerPage: perPage,
		Sort: q.Get("sort"), Order: q.Get("order")})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, items, httpx.Meta{Total: total, Page: page, PerPage: perPage})
}

func (a *apiHandlers) save(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	var req SaveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return
	}
	enabled := req.Enabled == nil || *req.Enabled
	rd, err := a.svc.Save(r.Context(), Redirect{ID: req.ID, FromPath: req.FromPath, ToURL: req.ToURL, Status: req.Status, Enabled: enabled})
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if req.ID == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, rd, nil)
}

func (a *apiHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, DeleteResponse{ID: id, Deleted: true}, nil)
}
