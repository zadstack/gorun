package menus

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// SaveRequest is the body of POST /v1/menus (create when ID is empty,
// update otherwise; items, when present, replace the tree).
type SaveRequest struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Locale   string `json:"locale,omitempty"`
	Items    []Item `json:"items,omitempty"`
}

// ItemsRequest is the body of PUT /v1/menus/{id}/items.
type ItemsRequest struct {
	Items []Item `json:"items"`
}

// DeleteResponse is returned by DELETE /v1/menus/{id}.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

const perm = "menus.manage"

func registerAPI(d *kernel.Deps, svc *service) {
	a := &apiHandlers{svc: svc}
	tags, scopes := []string{"menus"}, []string{perm}
	d.API.Add(kernel.APIOperation{Method: http.MethodGet, Path: "/v1/menus", Summary: "List menus with their items",
		Tags: tags, Scopes: scopes, Response: []Menu{}, Handler: a.list})
	d.API.Add(kernel.APIOperation{Method: http.MethodGet, Path: "/v1/menus/{id}", Summary: "Get a menu by ID or slug (items resolved for display)",
		Tags: tags, Scopes: scopes, Response: Menu{}, Handler: a.get,
		Params: []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true, Description: "menu ID or slug"}}})
	d.API.Add(kernel.APIOperation{Method: http.MethodPost, Path: "/v1/menus", Summary: "Create or update a menu",
		Tags: tags, Scopes: scopes, Request: SaveRequest{}, Response: Menu{}, Handler: a.save})
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true, Description: "menu ID"}}
	d.API.Add(kernel.APIOperation{Method: http.MethodPut, Path: "/v1/menus/{id}/items", Summary: "Replace a menu's item tree",
		Tags: tags, Scopes: scopes, Request: ItemsRequest{}, Response: Menu{}, Params: idParam, Handler: a.saveItems})
	d.API.Add(kernel.APIOperation{Method: http.MethodDelete, Path: "/v1/menus/{id}", Summary: "Delete a menu",
		Tags: tags, Scopes: scopes, Response: DeleteResponse{}, Params: idParam, Handler: a.remove})
}

type apiHandlers struct{ svc *service }

func allowed(w http.ResponseWriter, r *http.Request) bool {
	if !auth.Can(r.Context(), perm) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+perm, nil)
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid menu", ve.Fields)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "menu not found", nil)
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func (a *apiHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	menus, err := a.svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if menus == nil {
		menus = []Menu{}
	}
	httpx.JSON(w, http.StatusOK, menus, httpx.Meta{Total: len(menus), Page: 1, PerPage: len(menus)})
}

func (a *apiHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	key := chi.URLParam(r, "id")
	m, err := a.svc.Get(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		if m, err = a.svc.getRaw(r.Context(), key); err == nil {
			m.Items = a.svc.resolve(r.Context(), m.Items)
		}
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m, nil)
}

func (a *apiHandlers) save(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	var req SaveRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := a.svc.Save(r.Context(), Menu{ID: req.ID, Slug: req.Slug, Name: req.Name, Location: req.Location, Locale: req.Locale, Items: req.Items})
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if req.ID == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, m, nil)
}

func (a *apiHandlers) saveItems(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	var req ItemsRequest
	if !decode(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.svc.SaveItems(r.Context(), id, req.Items); err != nil {
		writeErr(w, err)
		return
	}
	m, err := a.svc.getRaw(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m, nil)
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
