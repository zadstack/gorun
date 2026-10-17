package webhooks

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

const maxBody = 64 << 10

// CreateResponse is returned once by POST /v1/webhooks and
// POST /v1/webhooks/{id}/secret: secret is never shown again.
type CreateResponse struct {
	Webhook Webhook `json:"webhook"`
	Secret  string  `json:"secret" doc:"whsec_…; sign check: sha256=hex(HMAC-SHA256(secret, timestamp + \".\" + body))"`
}

// DeleteResponse is returned by DELETE /v1/webhooks/{id}.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func registerAPI(d *kernel.Deps, svc *service) {
	a := &apiHandlers{svc: svc}
	tags, scopes := []string{"webhooks"}, []string{Perm}
	id := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	for _, op := range []kernel.APIOperation{
		{Method: http.MethodGet, Path: "/v1/webhooks/events", Summary: "List subscribable events", Tags: tags, Scopes: scopes,
			Response: []Event{}, Handler: a.events},
		{Method: http.MethodGet, Path: "/v1/webhooks", Summary: "List webhooks", Tags: tags, Scopes: scopes,
			Response: []Webhook{}, Handler: a.list},
		{Method: http.MethodPost, Path: "/v1/webhooks", Summary: "Create a webhook; the signing secret is returned once", Tags: tags, Scopes: scopes,
			Request: Input{}, Response: CreateResponse{}, Handler: a.create},
		{Method: http.MethodGet, Path: "/v1/webhooks/{id}", Summary: "Get a webhook", Tags: tags, Scopes: scopes,
			Params: id, Response: Webhook{}, Handler: a.get},
		{Method: http.MethodPatch, Path: "/v1/webhooks/{id}", Summary: "Update a webhook (omitted fields keep their value)", Tags: tags, Scopes: scopes,
			Params: id, Request: Input{}, Response: Webhook{}, Handler: a.update},
		{Method: http.MethodDelete, Path: "/v1/webhooks/{id}", Summary: "Delete a webhook and its delivery log", Tags: tags, Scopes: scopes,
			Params: id, Response: DeleteResponse{}, Handler: a.remove},
		{Method: http.MethodPost, Path: "/v1/webhooks/{id}/test", Summary: "Send a webhook.test event now (one attempt)", Tags: tags, Scopes: scopes,
			Params: id, Response: Delivery{}, Handler: a.test},
		{Method: http.MethodPost, Path: "/v1/webhooks/{id}/secret", Summary: "Rotate the signing secret; the new secret is returned once", Tags: tags, Scopes: scopes,
			Params: id, Response: CreateResponse{}, Handler: a.rotate},
		{Method: http.MethodGet, Path: "/v1/webhooks/{id}/deliveries", Summary: "List recent deliveries, newest first", Tags: tags, Scopes: scopes,
			Params: append(id, kernel.APIParam{Name: "limit", In: "query", Type: "integer", Description: "1–200, default 50"}), Response: []Delivery{}, Handler: a.deliveries},
		{Method: http.MethodPost, Path: "/v1/webhook-deliveries/{id}/redeliver", Summary: "Queue a past delivery's payload again", Tags: tags, Scopes: scopes,
			Params: id, Response: Delivery{}, Handler: a.redeliver},
	} {
		d.API.Add(op)
	}
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
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid webhook", ve.Fields)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "webhook not found", nil)
	case errors.Is(err, ErrInactive):
		httpx.Error(w, http.StatusConflict, "webhook_disabled", "enable the webhook first", nil)
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func (a *apiHandlers) events(w http.ResponseWriter, r *http.Request) {
	if allowed(w, r) {
		httpx.JSON(w, http.StatusOK, Events(), nil)
	}
}

func (a *apiHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	hooks, err := a.svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, Redacted(hooks), nil)
}

func (a *apiHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !allowed(w, r) || !decode(w, r, &in) {
		return
	}
	hook, secret, err := a.svc.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, CreateResponse{Webhook: *hook, Secret: secret}, nil)
}

func (a *apiHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	hook, err := a.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, hook, nil)
}

func (a *apiHandlers) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !allowed(w, r) || !decode(w, r, &in) {
		return
	}
	hook, err := a.svc.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, hook, nil)
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

func (a *apiHandlers) test(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	d, err := a.svc.Test(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d, nil)
}

func (a *apiHandlers) rotate(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	secret, err := a.svc.RotateSecret(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	hook, err := a.svc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, CreateResponse{Webhook: *hook, Secret: secret}, nil)
}

func (a *apiHandlers) deliveries(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.svc.Deliveries(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list, nil)
}

func (a *apiHandlers) redeliver(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	d, err := a.svc.Redeliver(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, d, nil)
}
