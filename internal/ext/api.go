package ext

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
)

// UninstallResponse is returned by DELETE /v1/extensions/{slug}.
type UninstallResponse struct {
	Slug   string `json:"slug"`
	Purged bool   `json:"purged"`
}

func registerAPI(d *kernel.Deps, s *service) {
	tags, scopes := []string{"extensions"}, []string{Perm}
	slugParam := kernel.APIParam{Name: "slug", In: "path", Type: "string", Required: true}
	d.API.Add(api.Op(http.MethodGet, "/v1/extensions", "List installed extensions", tags, scopes, nil, []Extension{},
		func(w http.ResponseWriter, r *http.Request) {
			list, err := s.List(r.Context())
			if err != nil {
				api.Fail(w, r, err)
				return
			}
			if list == nil {
				list = []Extension{}
			}
			api.WriteList(w, list, len(list), 1, max(len(list), 1))
		}))
	get := api.Op(http.MethodGet, "/v1/extensions/{slug}", "Get an extension", tags, scopes, nil, Extension{},
		func(w http.ResponseWriter, r *http.Request) {
			x, err := s.Status(r.Context(), chi.URLParam(r, "slug"))
			if err != nil {
				api.Fail(w, r, err)
				return
			}
			api.WriteItem(w, x)
		})
	get.Params = []kernel.APIParam{slugParam}
	d.API.Add(get)
	for _, verb := range []string{"activate", "deactivate"} {
		op := api.Op(http.MethodPost, "/v1/extensions/{slug}/"+verb, verb+" an extension", tags, scopes, nil, Extension{},
			func(w http.ResponseWriter, r *http.Request) {
				slug := chi.URLParam(r, "slug")
				fn := s.Activate
				if verb == "deactivate" {
					fn = s.Deactivate
				}
				if err := fn(r.Context(), slug); err != nil {
					api.Fail(w, r, err)
					return
				}
				x, err := s.Status(r.Context(), slug)
				if err != nil {
					api.Fail(w, r, err)
					return
				}
				api.WriteItem(w, x)
			})
		op.Params = []kernel.APIParam{slugParam}
		d.API.Add(op)
	}
	del := api.Op(http.MethodDelete, "/v1/extensions/{slug}", "Uninstall an extension", tags, scopes, nil, UninstallResponse{},
		func(w http.ResponseWriter, r *http.Request) {
			slug := chi.URLParam(r, "slug")
			purge, _ := strconv.ParseBool(r.URL.Query().Get("purge"))
			if err := s.Uninstall(r.Context(), slug, purge); err != nil {
				api.Fail(w, r, err)
				return
			}
			api.WriteItem(w, UninstallResponse{Slug: slug, Purged: purge})
		})
	del.Params = []kernel.APIParam{slugParam, {Name: "purge", In: "query", Type: "boolean",
		Description: "also delete the extension's content types, their content, its settings and package files"}}
	d.API.Add(del)
}
