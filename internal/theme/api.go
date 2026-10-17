package theme

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

type api struct{ s *service }

// ThemeDTO is a theme in API responses.
type ThemeDTO struct {
	Info
	Active bool `json:"active"`
}

// TemplateDTO is one template's sources in API responses; Draft is null
// when no draft exists.
type TemplateDTO struct {
	Name   string  `json:"name"`
	Source string  `json:"source"`
	Body   string  `json:"body"`
	Draft  *string `json:"draft"`
}

type TemplateListDTO struct {
	Templates []TemplateFile `json:"templates"`
	Overrides []Override     `json:"overrides"`
}

type draftInput struct {
	Body string `json:"body"`
}

// registerAPI adds the themes/templates operations (routed by the api
// module). Every handler checks themes.manage itself.
func registerAPI(d *kernel.Deps, s *service) {
	a := &api{s: s}
	scopes := []string{PermManage}
	nameParam := kernel.APIParam{Name: "name", In: "path", Type: "string", Required: true,
		Description: "Template name: single.html, parts.header.html, components.card.html"}
	for _, op := range []kernel.APIOperation{
		{Method: http.MethodGet, Path: "/v1/themes", Summary: "List installed themes", Handler: a.listThemes, Response: []ThemeDTO{}},
		{Method: http.MethodPost, Path: "/v1/themes/{slug}/activate", Summary: "Activate a theme", Handler: a.activate, Response: ThemeDTO{},
			Params: []kernel.APIParam{{Name: "slug", In: "path", Type: "string", Required: true}}},
		{Method: http.MethodGet, Path: "/v1/templates", Summary: "List the active theme's templates and overrides", Handler: a.listTemplates,
			Response: TemplateListDTO{}},
		{Method: http.MethodGet, Path: "/v1/templates/{name}", Summary: "Read a template (published source and draft)", Handler: a.readTemplate,
			Response: TemplateDTO{}, Params: []kernel.APIParam{nameParam}},
		{Method: http.MethodPut, Path: "/v1/templates/{name}/draft", Summary: "Save a template override draft (not live until published)",
			Handler: a.saveDraft, Request: draftInput{}, Response: TemplateDTO{}, Params: []kernel.APIParam{nameParam},
			Contract: &kernel.APIContract{MaxBodyBytes: maxTemplateBytes, Errors: []kernel.APIErrorResponse{{Status: 400, Code: "bad_request", Description: "One JSON object with a required, non-null string body; an explicit empty string is allowed. Invalid requests preserve the existing draft."}}}},
		{Method: http.MethodPost, Path: "/v1/templates/{name}/publish", Summary: "Publish a template override draft", Handler: a.publish,
			Response: TemplateDTO{}, Params: []kernel.APIParam{nameParam}},
		{Method: http.MethodDelete, Path: "/v1/templates/{name}/override", Summary: "Delete a template's override and draft (the theme file is used again)",
			Handler: a.deleteOverride, Response: TemplateDTO{}, Params: []kernel.APIParam{nameParam}},
	} {
		op.Tags, op.Scopes = []string{"themes"}, scopes
		d.API.Add(op)
	}
}

func (a *api) allowed(w http.ResponseWriter, r *http.Request) bool {
	if !auth.Can(r.Context(), PermManage) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "missing permission "+PermManage, nil)
		return false
	}
	return true
}

// fail maps service errors to the envelope.
func (a *api) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidName):
		httpx.Error(w, http.StatusBadRequest, "invalid_name", err.Error(), nil)
	case errors.Is(err, ErrInvalidTemplate):
		detail := "invalid template"
		var te *templateError
		if errors.As(err, &te) {
			detail = te.detail
		}
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "template does not parse", map[string]string{"body": detail})
	case errors.Is(err, ErrInvalidTheme):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error(), nil)
	default:
		a.s.log.Error("theme: api", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func templateName(r *http.Request) string {
	n := chi.URLParam(r, "name")
	if u, err := url.PathUnescape(n); err == nil {
		return u
	}
	return n
}

func (a *api) listThemes(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	list, err := a.s.List(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	active := a.s.activeSlug(r.Context())
	out := make([]ThemeDTO, len(list))
	for i, info := range list {
		out[i] = ThemeDTO{Info: info, Active: info.Slug == active}
	}
	httpx.JSON(w, http.StatusOK, out, map[string]any{"total": len(out)})
}

func (a *api) activate(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	if err := a.s.Activate(r.Context(), chi.URLParam(r, "slug")); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ThemeDTO{Info: a.s.Active(r.Context()), Active: true}, nil)
}

func (a *api) listTemplates(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	files, err := a.s.Templates(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	overrides, err := a.s.Overrides(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, TemplateListDTO{Templates: files, Overrides: overrides}, nil)
}

// dto returns name's published source (empty for draft-only names) and draft.
func (a *api) dto(r *http.Request, name string) (TemplateDTO, error) {
	return a.s.TemplateDetails(r.Context(), name)
}

// TemplateDetails returns the published source and any saved draft without publishing it.
func (s *service) TemplateDetails(ctx context.Context, name string) (TemplateDTO, error) {
	if !ValidName(name) {
		return TemplateDTO{}, ErrInvalidName
	}
	out := TemplateDTO{Name: name}
	body, err := s.ReadTemplate(ctx, name)
	switch {
	case err == nil:
		out.Body = body
	case !errors.Is(err, ErrNotFound):
		return out, err
	}
	if files, err := s.Templates(ctx); err == nil {
		for _, f := range files {
			if f.Name == name {
				out.Source = f.Source
			}
		}
	}
	if d, ok := s.readDraft(ctx, name); ok {
		out.Draft = &d
	}
	if out.Source == "" && out.Draft == nil {
		return out, err
	}
	return out, nil
}

func (a *api) readTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	name := templateName(r)
	if !ValidName(name) {
		a.fail(w, ErrInvalidName)
		return
	}
	out, err := a.dto(r, name)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

func (a *api) saveDraft(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	var in struct {
		Body *string `json:"body"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTemplateBytes))
	if err := decoder.Decode(&in); err != nil || in.Body == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "body must be JSON {\"body\": \"...\"}", nil)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "body must contain one JSON object", nil)
		return
	}
	name := templateName(r)
	if err := a.s.SaveOverride(r.Context(), name, *in.Body, true); err != nil {
		a.fail(w, err)
		return
	}
	out, err := a.dto(r, name)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

func (a *api) publish(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	name := templateName(r)
	if err := a.s.PublishOverride(r.Context(), name); err != nil {
		a.fail(w, err)
		return
	}
	out, err := a.dto(r, name)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

func (a *api) deleteOverride(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(w, r) {
		return
	}
	name := templateName(r)
	if err := a.s.DeleteOverride(r.Context(), name); err != nil {
		a.fail(w, err)
		return
	}
	out, err := a.dto(r, name)
	if errors.Is(err, ErrNotFound) { // an override-only template is gone entirely
		httpx.JSON(w, http.StatusOK, TemplateDTO{Name: name}, nil)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}
