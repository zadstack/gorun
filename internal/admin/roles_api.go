package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// roleDTO is the API representation of a role.
type roleDTO struct {
	Slug        string            `json:"slug"`
	Name        map[string]string `json:"name"`
	System      bool              `json:"system"`
	Permissions []string          `json:"permissions"`
	UserCount   int               `json:"user_count"`
}

func toRoleDTO(r *auth.Role) roleDTO {
	perms := r.Permissions
	if perms == nil {
		perms = []string{}
	}
	name := r.Name
	if name == nil {
		name = map[string]string{}
	}
	return roleDTO{
		Slug:        r.Slug,
		Name:        name,
		System:      r.System,
		Permissions: perms,
		UserCount:   r.UserCount,
	}
}

// roleCreateJSON is the POST /v1/roles body.
type roleCreateJSON struct {
	Slug        string            `json:"slug"`
	Name        map[string]string `json:"name"`
	NameEN      string            `json:"name_en"`
	NameAR      string            `json:"name_ar"`
	Permissions []string          `json:"permissions"`
}

// rolePatchJSON is the PATCH /v1/roles/{slug} body.
type rolePatchJSON struct {
	Name        map[string]string `json:"name"`
	NameEN      *string           `json:"name_en"`
	NameAR      *string           `json:"name_ar"`
	Permissions *[]string         `json:"permissions"`
}

func (h *handlers) roleAPIOperations() {
	slugParam := []kernel.APIParam{{Name: "slug", In: "path", Type: "string", Required: true}}
	ops := []kernel.APIOperation{
		{Method: "GET", Path: "/v1/roles", Summary: "List roles", Tags: []string{"roles"},
			Scopes: []string{PermUsers}, Response: []roleDTO{}, Handler: h.apiRolesList},
		{Method: "POST", Path: "/v1/roles", Summary: "Create a custom role", Tags: []string{"roles"},
			Scopes: []string{PermUsers}, Request: roleCreateJSON{}, Response: roleDTO{}, Handler: h.apiRoleCreate},
		{Method: "GET", Path: "/v1/roles/{slug}", Summary: "Get a role", Tags: []string{"roles"},
			Scopes: []string{PermUsers}, Params: slugParam, Response: roleDTO{}, Handler: h.apiRoleGet},
		{Method: "PATCH", Path: "/v1/roles/{slug}", Summary: "Update a role", Tags: []string{"roles"},
			Scopes: []string{PermUsers}, Params: slugParam, Request: rolePatchJSON{}, Response: roleDTO{}, Handler: h.apiRolePatch},
		{Method: "DELETE", Path: "/v1/roles/{slug}", Summary: "Delete a custom role", Tags: []string{"roles"},
			Scopes: []string{PermUsers}, Params: slugParam, Response: opResult{}, Handler: h.apiRoleDelete},
	}
	for _, op := range ops {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiRolesList(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	list, err := h.roles.List(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	out := make([]roleDTO, 0, len(list))
	for i := range list {
		out = append(out, toRoleDTO(&list[i]))
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

func (h *handlers) apiRoleGet(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	role, err := h.roles.Get(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, auth.ErrRoleNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toRoleDTO(role), nil)
}

func (h *handlers) apiRoleCreate(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	var in roleCreateJSON
	if err := decodeJSON(r, &in); err != nil {
		h.badRequest(w, r, err)
		return
	}
	role, fields, err := h.createRole(r.Context(), roleInput{
		Slug: in.Slug, Name: in.Name, NameEN: in.NameEN, NameAR: in.NameAR, Permissions: in.Permissions,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(fields) > 0 {
		h.validationFailed(w, r, fields)
		return
	}
	httpx.JSON(w, http.StatusCreated, toRoleDTO(role), nil)
}

func (h *handlers) apiRolePatch(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	slug := chi.URLParam(r, "slug")
	cur, err := h.roles.Get(r.Context(), slug)
	if errors.Is(err, auth.ErrRoleNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	var body map[string]json.RawMessage
	if err := decodeJSON(r, &body); err != nil {
		h.badRequest(w, r, err)
		return
	}
	in := roleInput{
		Slug:        cur.Slug,
		NameEN:      cur.Name["en"],
		NameAR:      cur.Name["ar"],
		Permissions: append([]string(nil), cur.Permissions...),
	}
	touchedPerms := false
	for k, raw := range body {
		switch k {
		case "name":
			var name map[string]string
			if err := json.Unmarshal(raw, &name); err != nil {
				h.validationFailed(w, r, map[string]string{"name": "admin.api.invalid_value"})
				return
			}
			in.Name = name
			if v := name["en"]; v != "" {
				in.NameEN = v
			}
			if v := name["ar"]; v != "" {
				in.NameAR = v
			}
		case "name_en":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				h.validationFailed(w, r, map[string]string{"name_en": "admin.api.invalid_value"})
				return
			}
			in.NameEN = v
		case "name_ar":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				h.validationFailed(w, r, map[string]string{"name_ar": "admin.api.invalid_value"})
				return
			}
			in.NameAR = v
		case "permissions":
			var perms []string
			if err := json.Unmarshal(raw, &perms); err != nil {
				h.validationFailed(w, r, map[string]string{"permissions": "admin.api.invalid_value"})
				return
			}
			in.Permissions = perms
			touchedPerms = true
		default:
			h.validationFailed(w, r, map[string]string{k: "admin.api.unknown_setting"})
			return
		}
	}
	_ = touchedPerms
	// API does not use session reauth; users.manage is the gate (same as user delete).
	name, perms := roleNamePerms(in)
	permsChanged := !sameStringSet(cur.Permissions, perms)
	if permsChanged && cur.Slug != auth.RoleAdmin {
		if fields := h.guardRoleGrants(r.Context(), perms); len(fields) > 0 {
			h.validationFailed(w, r, fields)
			return
		}
	}
	patch := auth.RolePatch{Name: name}
	if permsChanged {
		patch.Permissions = &perms
	}
	role, err := h.roles.Update(r.Context(), cur.Slug, patch)
	if err != nil {
		if f, ok := roleServiceFields(err); ok {
			h.validationFailed(w, r, f)
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toRoleDTO(role), nil)
}

func (h *handlers) apiRoleDelete(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	slug := chi.URLParam(r, "slug")
	if err := h.deleteRole(r.Context(), slug); err != nil {
		switch {
		case errors.Is(err, auth.ErrRoleNotFound):
			httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
		case errors.Is(err, auth.ErrSystemRole):
			httpx.Error(w, http.StatusConflict, "conflict", h.tr(r, "admin.roles.error_system"), nil)
		case errors.Is(err, auth.ErrRoleInUse):
			httpx.Error(w, http.StatusConflict, "conflict", h.tr(r, "admin.roles.error_in_use"), nil)
		default:
			h.serverError(w, r, err)
		}
		return
	}
	httpx.JSON(w, http.StatusOK, opResult{ID: slug, Status: "deleted"}, nil)
}
