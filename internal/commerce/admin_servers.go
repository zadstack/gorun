package commerce

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
)

func (h *adminHandlers) servers(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Servers(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/commerce/servers.html", map[string]any{"Servers": list})
}

type serverForm struct {
	Name     string `form:"name"`
	BaseURL  string `form:"base_url"`
	ClientID string `form:"client_id"`
	Secret   string `form:"secret"`
}

type serverPage struct {
	ID        string
	SecretSet bool
	Form      serverForm
	Fields    map[string]string
	Server    *Server
}

func (h *adminHandlers) renderServer(w http.ResponseWriter, r *http.Request, status int, pg serverPage) {
	if pg.Fields == nil {
		pg.Fields = map[string]string{}
	}
	pg.Form.Secret = "" // never echo a secret
	h.tpl.RenderWith(w, r, status, "admin", "pages/commerce/server_form.html", pg)
}

func (h *adminHandlers) serverNew(w http.ResponseWriter, r *http.Request) {
	h.renderServer(w, r, http.StatusOK, serverPage{})
}

func (h *adminHandlers) serverEdit(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Server(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderServer(w, r, http.StatusOK, serverPage{ID: s.ID, SecretSet: s.SecretSet, Server: s,
		Form: serverForm{Name: s.Name, BaseURL: s.BaseURL, ClientID: s.ClientID}})
}

func (h *adminHandlers) serverSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var f serverForm
	_ = httpx.Bind(r, &f)
	saved, err := h.svc.saveServer(r.Context(), Server{ID: id, Name: f.Name, BaseURL: f.BaseURL, ClientID: f.ClientID, Secret: f.Secret})
	var ve *ValidationError
	if errors.As(err, &ve) {
		pg := serverPage{ID: id, Form: f, Fields: ve.Fields}
		if id != "" {
			if s, err := h.svc.Server(r.Context(), id); err == nil {
				pg.SecretSet, pg.Server = s.SecretSet, s
			}
		}
		h.renderServer(w, r, http.StatusUnprocessableEntity, pg)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "commerce.flash.saved")
	http.Redirect(w, r, "/admin/commerce/servers/"+saved.ID, http.StatusSeeOther)
}

func (h *adminHandlers) serverDelete(w http.ResponseWriter, r *http.Request) {
	err := h.svc.DeleteServer(r.Context(), chi.URLParam(r, "id"))
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.SetFlash(w, "error", "commerce.error.server_in_use")
	case err != nil:
		h.fail(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", "commerce.flash.deleted")
	}
	http.Redirect(w, r, "/admin/commerce/servers", http.StatusSeeOther)
}

// serverTest is the HTMX "Test connection" action: it renders the result
// (plans or the error) into the server page.
func (h *adminHandlers) serverTest(w http.ResponseWriter, r *http.Request) {
	cat, err := h.svc.TestServer(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	data := map[string]any{"OK": err == nil, "Catalog": cat, "Error": ""}
	if err != nil {
		data["Error"] = err.Error()
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/commerce/server_test.html", data)
}
