package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

// roleInput is a role create/update request from a form or JSON.
type roleInput struct {
	Slug        string            `form:"slug" json:"slug"`
	NameEN      string            `form:"name_en" json:"name_en"`
	NameAR      string            `form:"name_ar" json:"name_ar"`
	Permissions []string          `form:"permissions" json:"permissions"`
	Name        map[string]string `json:"name"` // API alternate: {"en":"…","ar":"…"}
}

type rolesListPage struct {
	Roles  []roleListItem
	Tasks  []roleTaskRow
	Locale string
}

type roleListItem struct {
	auth.Role
	DisplayName    string
	AllPermissions bool
}

type roleEditPage struct {
	Record      *auth.Role
	Form        roleInput
	Fields      map[string]string
	Groups      []scopeGroup
	AdminLocked bool
	IsNew       bool
}

func (h *handlers) rolesList(w http.ResponseWriter, r *http.Request) {
	list, err := h.roles.List(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	locale := i18n.Locale(r.Context())
	items := make([]roleListItem, 0, len(list))
	for _, role := range list {
		items = append(items, roleListItem{Role: role, DisplayName: roleDisplayName(role.Name, locale), AllPermissions: slices.Contains(role.Permissions, "*")})
	}
	h.tpl.Render(w, r, "pages/roles/list.html", rolesListPage{Roles: items, Locale: locale, Tasks: roleTaskRows(list)})
}

func (h *handlers) roleNew(w http.ResponseWriter, r *http.Request) {
	form := roleInput{}
	if clone := strings.TrimSpace(r.URL.Query().Get("clone")); clone != "" {
		if src, err := h.roles.Get(r.Context(), clone); err == nil {
			form.Permissions = append([]string(nil), src.Permissions...)
			// Drop wildcard if cloning admin — custom roles cannot hold *.
			filtered := form.Permissions[:0]
			for _, p := range form.Permissions {
				if p != "*" {
					filtered = append(filtered, p)
				}
			}
			form.Permissions = filtered
		}
	}
	h.renderRoleForm(w, r, http.StatusOK, roleEditPage{Form: form, IsNew: true})
}

func (h *handlers) roleCreate(w http.ResponseWriter, r *http.Request) {
	var in roleInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	role, fields, err := h.createRole(r.Context(), in)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(fields) > 0 {
		h.renderRoleForm(w, r, http.StatusUnprocessableEntity, roleEditPage{Form: in, Fields: fields, IsNew: true})
		return
	}
	httpx.SetFlash(w, "success", "admin.roles.created_flash")
	redirect(w, r, "/admin/roles/"+role.Slug)
}

func (h *handlers) roleEdit(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	role, err := h.roles.Get(r.Context(), slug)
	if errors.Is(err, auth.ErrRoleNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderRoleForm(w, r, http.StatusOK, roleEditPage{
		Record: role,
		Form: roleInput{
			Slug:        role.Slug,
			NameEN:      role.Name["en"],
			NameAR:      role.Name["ar"],
			Permissions: append([]string(nil), role.Permissions...),
		},
		AdminLocked: role.Slug == auth.RoleAdmin,
	})
}

func (h *handlers) roleUpdate(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	role, err := h.roles.Get(r.Context(), slug)
	if errors.Is(err, auth.ErrRoleNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	var in roleInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	in.Slug = role.Slug
	updated, fields, needReauth, err := h.updateRole(r.Context(), role, in)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if needReauth {
		http.Redirect(w, r, "/admin/reauth?next="+url.QueryEscape("/admin/roles/"+slug), http.StatusSeeOther)
		return
	}
	if len(fields) > 0 {
		h.renderRoleForm(w, r, http.StatusUnprocessableEntity, roleEditPage{
			Record: role, Form: in, Fields: fields, AdminLocked: role.Slug == auth.RoleAdmin,
		})
		return
	}
	httpx.SetFlash(w, "success", "admin.roles.saved")
	redirect(w, r, "/admin/roles/"+updated.Slug)
}

func (h *handlers) roleDelete(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	editPath := "/admin/roles/" + slug
	// Recent password confirmation is required; after reauth the operator
	// returns to the edit page (POST cannot be replayed through /admin/reauth).
	if !auth.RecentlyAuthenticated(r.Context(), auth.ReauthWindow) {
		http.Redirect(w, r, "/admin/reauth?next="+url.QueryEscape(editPath), http.StatusSeeOther)
		return
	}
	if err := h.deleteRole(r.Context(), slug); err != nil {
		switch {
		case errors.Is(err, auth.ErrRoleNotFound):
			http.NotFound(w, r)
		case errors.Is(err, auth.ErrSystemRole):
			httpx.SetFlash(w, "error", "admin.roles.error_system")
			redirect(w, r, editPath)
		case errors.Is(err, auth.ErrRoleInUse):
			httpx.SetFlash(w, "error", "admin.roles.error_in_use")
			redirect(w, r, editPath)
		default:
			h.serverError(w, r, err)
		}
		return
	}
	httpx.SetFlash(w, "success", "admin.roles.deleted")
	redirect(w, r, "/admin/roles")
}

func (h *handlers) renderRoleForm(w http.ResponseWriter, r *http.Request, status int, data roleEditPage) {
	data.Groups = h.scopeGroups(r, data.Form.Permissions)
	h.tpl.RenderWith(w, r, status, "admin", "pages/roles/edit.html", data)
}

func (h *handlers) createRole(ctx context.Context, in roleInput) (*auth.Role, map[string]string, error) {
	name, perms := roleNamePerms(in)
	if fields := h.guardRoleGrants(ctx, perms); len(fields) > 0 {
		return nil, fields, nil
	}
	role, err := h.roles.Create(ctx, in.Slug, name, perms)
	if err != nil {
		if f, ok := roleServiceFields(err); ok {
			return nil, f, nil
		}
		return nil, nil, err
	}
	return role, nil, nil
}

func (h *handlers) updateRole(ctx context.Context, cur *auth.Role, in roleInput) (*auth.Role, map[string]string, bool, error) {
	name, perms := roleNamePerms(in)
	permsChanged := !sameStringSet(cur.Permissions, perms)
	if permsChanged {
		if !auth.RecentlyAuthenticated(ctx, auth.ReauthWindow) {
			return nil, nil, true, nil
		}
		if cur.Slug != auth.RoleAdmin {
			if fields := h.guardRoleGrants(ctx, perms); len(fields) > 0 {
				return nil, fields, false, nil
			}
		}
	}
	patch := auth.RolePatch{Name: name}
	if permsChanged {
		patch.Permissions = &perms
	}
	role, err := h.roles.Update(ctx, cur.Slug, patch)
	if err != nil {
		if f, ok := roleServiceFields(err); ok {
			return nil, f, false, nil
		}
		return nil, nil, false, err
	}
	return role, nil, false, nil
}

func (h *handlers) deleteRole(ctx context.Context, slug string) error {
	return h.roles.Delete(ctx, slug)
}

func (h *handlers) guardRoleGrants(ctx context.Context, perms []string) map[string]string {
	if !auth.CanGrant(ctx, perms) {
		return map[string]string{"permissions": "auth.error.role_grant_denied"}
	}
	return nil
}

func roleNamePerms(in roleInput) (map[string]string, []string) {
	name := map[string]string{}
	if in.Name != nil {
		name = in.Name
	}
	if strings.TrimSpace(in.NameEN) != "" {
		name["en"] = strings.TrimSpace(in.NameEN)
	}
	if strings.TrimSpace(in.NameAR) != "" {
		name["ar"] = strings.TrimSpace(in.NameAR)
	}
	return name, append([]string(nil), in.Permissions...)
}

func roleServiceFields(err error) (map[string]string, bool) {
	var ve *auth.ValidationError
	if errors.As(err, &ve) {
		return ve.Fields, true
	}
	return nil, false
}

func roleDisplayName(names map[string]string, locale string) string {
	if n := names[locale]; n != "" {
		return n
	}
	if n := names["en"]; n != "" {
		return n
	}
	return ""
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}

type roleTaskRow struct {
	LabelKey string
	Grants   []bool
}

func roleTaskRows(roles []auth.Role) []roleTaskRow {
	specs := []struct{ key, permission string }{
		{"edit", "content.edit"}, {"publish", "content.publish"}, {"keys", PermAPIKeys}, {"users", PermUsers}, {"settings", PermSettings}, {"backups", PermBackup},
	}
	rows := make([]roleTaskRow, 0, len(specs))
	for _, task := range specs {
		row := roleTaskRow{LabelKey: "operator.task." + task.key}
		for _, role := range roles {
			row.Grants = append(row.Grants, slices.Contains(role.Permissions, "*") || slices.Contains(role.Permissions, task.permission))
		}
		rows = append(rows, row)
	}
	return rows
}
