package services

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed catalog/*.json
var catalogs embed.FS

// ModuleName is the services module's name.
const ModuleName = "services"

// permManage is the permission the one-time secret page needs.
const permManage = "settings.manage"

// secretPath is the one-time secret page (under /admin).
const secretPath = "/services/{name}/secret"

// Module serves the one-time secret page of the services.
type Module struct {
	d   *kernel.Deps
	reg *Registry
	tpl *httpx.Templates
}

// New returns the services module.
func New() kernel.Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return ModuleName }

// Migrations implements kernel.Module: links live in the settings table.
func (*Module) Migrations() fs.FS { return nil }

// Catalogs implements kernel.CatalogProvider (the services.* strings).
func (*Module) Catalogs() fs.FS { return catalogs }

// Register publishes the registry (ServiceRegistry) for the MCP toolset,
// adds the REST operations under /site/services, and GET and POST
// /admin/services/{name}/secret behind sign-in and settings.manage (CSRF
// comes with the admin router). Without the admin renderer there is no
// secret page.
func (m *Module) Register(d *kernel.Deps) error {
	m.d, m.reg = d, Default(d)
	d.Services.Set(ServiceRegistry, m.reg)
	registerAPI(d, m.reg)
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return nil
	}
	m.tpl = tpl
	s := d.Router.Admin.With(auth.RequirePerm(permManage))
	s.Get(secretPath, m.secretPage)
	s.Post(secretPath, m.saveSecret)
	return nil
}

// secretData is pages/services/secret.html: State is form, done or gone.
type secretData struct {
	Name, Secret, Token, State string
	Fields                     map[string]string
}

// service is the {name} service, or a 404 written.
func (m *Module) service(w http.ResponseWriter, r *http.Request) (Service, bool) {
	s, ok := m.reg.Get(chi.URLParam(r, "name"))
	if !ok {
		m.tpl.RenderError(w, r, http.StatusNotFound)
	}
	return s, ok
}

// render writes the page; it is never cached and never sends its URL (the
// token) as a Referer.
func (m *Module) render(w http.ResponseWriter, r *http.Request, status int, data secretData) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	m.tpl.RenderWith(w, r, status, "admin", "pages/services/secret.html", data)
}

func (m *Module) secretPage(w http.ResponseWriter, r *http.Request) {
	s, ok := m.service(w, r)
	if !ok {
		return
	}
	data := secretData{Name: s.Name(), Secret: s.Secret(""), Token: r.URL.Query().Get("t"), State: "form"}
	if err := m.reg.checkLink(r.Context(), data.Token, data.Name); err != nil {
		if !errors.Is(err, ErrLinkGone) {
			m.fail(w, r, err)
			return
		}
		data.State, data.Token = "gone", ""
		m.render(w, r, http.StatusGone, data)
		return
	}
	m.render(w, r, http.StatusOK, data)
}

// saveSecret redeems the link with the typed secret, which is never written
// back into the page or a log.
func (m *Module) saveSecret(w http.ResponseWriter, r *http.Request) {
	s, ok := m.service(w, r)
	if !ok {
		return
	}
	data := secretData{Name: s.Name(), Secret: s.Secret(""), Token: r.PostFormValue("t"), State: "done"}
	_, fields, err := m.reg.RedeemSecret(r.Context(), data.Token, data.Name, r.PostFormValue("secret"), "admin")
	switch {
	case errors.Is(err, ErrLinkGone):
		data.State, data.Token = "gone", ""
		m.render(w, r, http.StatusGone, data)
	case err != nil:
		m.fail(w, r, err)
	case len(fields) > 0:
		data.State, data.Fields = "form", fields
		m.render(w, r, http.StatusUnprocessableEntity, data)
	default:
		data.Token = ""
		m.render(w, r, http.StatusOK, data)
	}
}

func (m *Module) fail(w http.ResponseWriter, r *http.Request, err error) {
	m.d.Log.Error("services: secret page", "service", chi.URLParam(r, "name"), "err", err)
	m.tpl.RenderError(w, r, http.StatusInternalServerError)
}
