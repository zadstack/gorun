package webhooks

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

type adminHandlers struct {
	svc *service
	tpl *httpx.Templates
	log *slog.Logger
}

type hookForm struct {
	Name   string   `form:"name"`
	URL    string   `form:"url"`
	Events []string `form:"events"`
	Active bool     `form:"active"`
}

type eventChoice struct {
	Name, Label string
	Checked     bool
}

type listPage struct {
	Hooks  []Webhook
	Form   hookForm
	Fields map[string]string
	Events []eventChoice
	Secret string // shown once after create
	NewID  string
}

type detailPage struct {
	Hook       *Webhook
	Form       hookForm
	Fields     map[string]string
	Events     []eventChoice
	Deliveries []Delivery
	Secret     string    // shown once after a secret rotation
	Tested     *Delivery // result of a test just sent
}

func eventChoices(r *http.Request, checked []string) []eventChoice {
	locale := i18n.Locale(r.Context())
	if locale != "ar" {
		locale = "en"
	}
	out := []eventChoice{{Name: "*", Label: map[string]string{"en": "Every event", "ar": "كل الأحداث"}[locale], Checked: contains(checked, "*")}}
	for _, e := range events {
		out = append(out, eventChoice{Name: e.Name, Label: e.Label[locale], Checked: contains(checked, e.Name)})
	}
	return out
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.log.Error("webhooks: admin", "err", err)
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}

func (h *adminHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, p listPage) {
	hooks, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p.Hooks, p.Events = Redacted(hooks), eventChoices(r, p.Form.Events)
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/webhooks/list.html", p)
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, listPage{Form: hookForm{Active: true}})
}

func (h *adminHandlers) create(w http.ResponseWriter, r *http.Request) {
	var f hookForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	active := true
	hook, secret, err := h.svc.Create(r.Context(), Input{Name: &f.Name, URL: &f.URL, Events: f.Events, Active: &active})
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		h.renderList(w, r, http.StatusUnprocessableEntity, listPage{Form: f, Fields: ve.Fields})
	case err != nil:
		h.fail(w, r, err)
	default:
		// The secret is rendered once in this response; never flashed or redirected.
		h.renderList(w, r, http.StatusCreated, listPage{Form: hookForm{Active: true}, Secret: secret, NewID: hook.ID})
	}
}

func (h *adminHandlers) renderDetail(w http.ResponseWriter, r *http.Request, status int, p detailPage) {
	id := chi.URLParam(r, "id")
	hook, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if p.Form.URL == "" && p.Fields == nil {
		p.Form = hookForm{Name: hook.Name, URL: hook.URL, Events: hook.Events, Active: hook.Active}
	}
	if p.Deliveries, err = h.svc.Deliveries(r.Context(), id, 50); err != nil {
		h.fail(w, r, err)
		return
	}
	p.Hook, p.Events = hook, eventChoices(r, p.Form.Events)
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/webhooks/detail.html", p)
}

func (h *adminHandlers) detail(w http.ResponseWriter, r *http.Request) {
	h.renderDetail(w, r, http.StatusOK, detailPage{})
}

func (h *adminHandlers) update(w http.ResponseWriter, r *http.Request) {
	var f hookForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	events := f.Events
	if events == nil {
		events = []string{}
	}
	_, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), Input{Name: &f.Name, URL: &f.URL, Events: events, Active: &f.Active})
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		h.renderDetail(w, r, http.StatusUnprocessableEntity, detailPage{Form: f, Fields: ve.Fields})
	case err != nil:
		h.fail(w, r, err)
	default:
		httpx.SetFlash(w, "success", "webhooks.saved")
		http.Redirect(w, r, "/admin/webhooks/"+chi.URLParam(r, "id"), http.StatusSeeOther)
	}
}

func (h *adminHandlers) test(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Test(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrInactive) {
		httpx.SetFlash(w, "error", "webhooks.test_inactive")
		http.Redirect(w, r, "/admin/webhooks/"+chi.URLParam(r, "id"), http.StatusSeeOther)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderDetail(w, r, http.StatusOK, detailPage{Tested: d})
}

func (h *adminHandlers) rotate(w http.ResponseWriter, r *http.Request) {
	secret, err := h.svc.RotateSecret(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderDetail(w, r, http.StatusOK, detailPage{Secret: secret})
}

func (h *adminHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "webhooks.deleted")
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

func (h *adminHandlers) redeliver(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Redeliver(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrInactive) {
		back := "/admin/webhooks"
		if old, err := h.svc.delivery(r.Context(), chi.URLParam(r, "id")); err == nil {
			back += "/" + old.WebhookID
		}
		httpx.SetFlash(w, "error", "webhooks.redeliver_inactive")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "webhooks.redelivered")
	http.Redirect(w, r, "/admin/webhooks/"+d.WebhookID, http.StatusSeeOther)
}
