package admin

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/observe"
)

// Observations API: pages and GET operations read saved results only; a
// refresh queues background work and answers 202 with a task reference.

var observationErrors = []kernel.APIErrorResponse{
	{Status: 404, Code: "not_found", Description: "Unknown capability kind, or the key no longer exists in configuration"},
	{Status: 409, Code: "capability_disabled", Description: "The capability is disabled; saved observations stay readable but no refresh runs"},
}

func (h *handlers) observationAPIOperations() {
	keyParams := []kernel.APIParam{{Name: "kind", In: "path", Type: "string", Required: true}, {Name: "key", In: "path", Type: "string", Required: true}}
	list := api.Op("GET", "/system/observations", "Saved background observations of expensive or external checks, with age and attempt state (no external calls)",
		[]string{"system"}, []string{PermHealth}, nil, []observe.Observation{}, h.apiObservationsList)
	list.Params = []kernel.APIParam{{Name: "kind", In: "query", Type: "string", Description: "Restrict to one capability kind"}}
	get := api.Op("GET", "/system/observations/{kind}/{key}", "One saved observation; status never is a key that was never refreshed",
		[]string{"system"}, []string{PermHealth}, nil, observe.Observation{}, h.apiObservationGet)
	get.Params = keyParams
	get.Contract = &kernel.APIContract{Errors: observationErrors[:1]}
	refresh := api.Op("POST", "/system/observations/{kind}/{key}/refresh", "Queue a background refresh and return its task reference at once; concurrent requests share one job",
		[]string{"system"}, []string{PermSettings}, nil, observe.Ref{}, h.apiObservationRefresh)
	refresh.Params = keyParams
	refresh.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: observationErrors}
	for _, op := range []kernel.APIOperation{list, get, refresh} {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiObservationsList(w http.ResponseWriter, r *http.Request) {
	var (
		list []observe.Observation
		err  error
	)
	if kind := r.URL.Query().Get("kind"); kind != "" {
		list, err = h.observe.List(r.Context(), kind)
		if errors.Is(err, observe.ErrUnknownKind) {
			api.NotFound(w)
			return
		}
	} else {
		list, err = h.observe.All(r.Context())
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, list)
}

func (h *handlers) apiObservationGet(w http.ResponseWriter, r *http.Request) {
	o, err := h.observe.Get(r.Context(), chi.URLParam(r, "kind"), chi.URLParam(r, "key"))
	if errors.Is(err, observe.ErrUnknownKind) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, o)
}

func (h *handlers) apiObservationRefresh(w http.ResponseWriter, r *http.Request) {
	ref, err := h.observe.Refresh(r.Context(), chi.URLParam(r, "kind"), chi.URLParam(r, "key"))
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusAccepted, ref, nil)
	case errors.Is(err, observe.ErrUnknownKind):
		api.NotFound(w)
	case errors.Is(err, observe.ErrDisabled):
		api.WriteError(w, http.StatusConflict, "capability_disabled", "the capability is disabled; enable it and restart before refreshing", nil)
	default:
		api.Fail(w, r, err)
	}
}

func chiParam(r *http.Request, name string) string { return chi.URLParam(r, name) }
