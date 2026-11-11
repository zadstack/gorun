package auth

import (
	"context"
	"net/http"
	"slices"

	"github.com/zadstack/gorun/internal/i18n"
)

// twoFactorRole is one checkbox of the enforcement form.
type twoFactorRole struct {
	Slug, Name string
	Required   bool
	Users      int
}

// twoFactorRoles lists staff roles (customers never use the admin) with
// their enforcement state.
func (h *handlers) twoFactorRoles(ctx context.Context) ([]twoFactorRole, error) {
	roles, err := NewRoleService(h.deps.DB, nil, nil).List(ctx)
	if err != nil {
		return nil, err
	}
	required := TwoFactorRequiredRoles(ctx, h.deps)
	locale := i18n.Locale(ctx)
	out := make([]twoFactorRole, 0, len(roles))
	for _, r := range roles {
		if r.Slug == RoleCustomer {
			continue
		}
		name := r.Name[locale]
		if name == "" {
			name = r.Name["en"]
		}
		if name == "" {
			name = r.Slug
		}
		out = append(out, twoFactorRole{Slug: r.Slug, Name: name, Required: slices.Contains(required, r.Slug), Users: r.UserCount})
	}
	return out, nil
}

// securityTwoFactorSave stores the roles that must use two-factor
// authentication. Unknown slugs and the customer role are ignored.
func (h *handlers) securityTwoFactorSave(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		h.securityRender(w, r, 422, "auth.security.invalid")
		return
	}
	roles, err := h.twoFactorRoles(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	selected := r.PostForm["roles"]
	required := []string{}
	for _, role := range roles {
		if slices.Contains(selected, role.Slug) {
			required = append(required, role.Slug)
		}
	}
	if err := h.deps.Settings.Set(r.Context(), TwoFactorRolesSetting, required); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.deps.Audit.Record(r.Context(), "security.two_factor_roles", "setting", TwoFactorRolesSetting, map[string][]string{"roles": required})
	http.Redirect(w, r, "/admin/security/login-attempts#two-factor", http.StatusSeeOther)
}
