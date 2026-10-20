package admin

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
)

func (h *handlers) canvasReadAPIOperations() {
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	for _, op := range []kernel.APIOperation{
		{
			Method: "GET", Path: "/v1/canvases", Summary: "List readable canvas boards (owner boards or grant-scoped)",
			Tags: []string{"canvases"}, Scopes: []string{"canvas.read", "content.view"},
			Params:   append([]kernel.APIParam{}, api.PageParams[:2]...),
			Response: []CanvasSummary{}, Handler: h.apiCanvasesList,
		},
		{
			Method: "GET", Path: "/v1/canvases/{id}", Summary: "Get a readable canvas board document (grant-filtered for agent keys; REST refuses agent keys)",
			Tags: []string{"canvases"}, Scopes: []string{"canvas.read", "content.view"},
			Params: idParam, Response: CanvasDocument{}, Handler: h.apiCanvasesGet,
		},
	} {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiCanvasesList(w http.ResponseWriter, r *http.Request) {
	page, perPage := api.Pagination(r)
	items, total, err := h.canvasListReadable(r.Context(), page, perPage)
	if err != nil {
		h.canvasReadAPIError(w, r, err)
		return
	}
	api.WriteList(w, items, total, page, perPage)
}

func (h *handlers) apiCanvasesGet(w http.ResponseWriter, r *http.Request) {
	doc, err := h.canvasGetReadable(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.canvasReadAPIError(w, r, err)
		return
	}
	api.WriteItem(w, doc)
}

func (h *handlers) canvasReadAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var coded interface {
		ErrorCode() string
		Error() string
	}
	if errors.As(err, &coded) {
		switch coded.ErrorCode() {
		case "forbidden":
			api.WriteError(w, http.StatusForbidden, api.CodeForbidden, coded.Error(), nil)
			return
		case "not_found":
			api.WriteError(w, http.StatusNotFound, api.CodeNotFound, coded.Error(), nil)
			return
		case "validation_failed":
			api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, coded.Error(), nil)
			return
		}
	}
	h.serverError(w, r, err)
}
