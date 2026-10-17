package admin

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
)

// userInput is a user create/update request from a form or JSON.
type userInput struct {
	Name     string `form:"name" json:"name"`
	Email    string `form:"email" json:"email"`
	Role     string `form:"role" json:"role"`
	Locale   string `form:"locale" json:"locale"`
	Status   string `form:"status" json:"status"`
	Password string `form:"password" json:"password"`
}

func validStatus(s string) bool { return s == auth.StatusActive || s == auth.StatusDisabled }

// serviceFields maps auth service errors to field errors (i18n keys); ok
// is false for unexpected errors.
func serviceFields(err error) (map[string]string, bool) {
	var ve *auth.ValidationError
	switch {
	case errors.As(err, &ve):
		return maps.Clone(ve.Fields), true
	case errors.Is(err, auth.ErrEmailTaken):
		return map[string]string{"email": "auth.error.email_taken"}, true
	}
	return nil, false
}

// createUser creates a user, then applies locale and status. It returns
// field errors (i18n keys) for invalid input.
func (h *handlers) createUser(ctx context.Context, in userInput) (*auth.User, map[string]string, error) {
	fields := map[string]string{}
	if in.Locale != "" && !validLocale(in.Locale) {
		fields["locale"] = "admin.users.error_locale"
	}
	if in.Status != "" && !validStatus(in.Status) {
		fields["status"] = "auth.error.status_invalid"
	}
	if len(fields) > 0 {
		return nil, fields, nil
	}
	u, err := h.users.Create(ctx, in.Email, in.Name, in.Password, in.Role)
	if err != nil {
		if f, ok := serviceFields(err); ok {
			return nil, f, nil
		}
		return nil, nil, err
	}
	var p auth.UserPatch
	if in.Locale != "" && in.Locale != u.Locale {
		p.Locale = &in.Locale
	}
	if in.Status != "" && in.Status != u.Status {
		p.Status = &in.Status
	}
	if p.Locale == nil && p.Status == nil {
		return u, nil, nil
	}
	u, err = h.users.Update(ctx, u.ID, p)
	return u, nil, err
}

// diffPatch returns a patch with only the fields that differ from cur
// (the password only when given).
func diffPatch(cur *auth.User, in userInput) auth.UserPatch {
	var p auth.UserPatch
	set := func(dst **string, now, want string) {
		if want != now {
			v := want
			*dst = &v
		}
	}
	set(&p.Name, cur.Name, strings.TrimSpace(in.Name))
	set(&p.Email, cur.Email, strings.ToLower(strings.TrimSpace(in.Email)))
	set(&p.Role, cur.Role, in.Role)
	set(&p.Locale, cur.Locale, in.Locale)
	set(&p.Status, cur.Status, in.Status)
	if in.Password != "" {
		pw := in.Password
		p.Password = &pw
	}
	return p
}

// updateUser applies p to target after the self / last-admin guards and
// audits a role change with from/to.
func (h *handlers) updateUser(ctx context.Context, target *auth.User, p auth.UserPatch) (*auth.User, map[string]string, error) {
	if p.Locale != nil && !validLocale(*p.Locale) {
		return nil, map[string]string{"locale": "admin.users.error_locale"}, nil
	}
	// Guard and mutation run under userMu against a fresh copy of the
	// target, so two concurrent demotions cannot both pass the last-admin
	// check.
	h.userMu.Lock()
	defer h.userMu.Unlock()
	fresh, err := h.users.Get(ctx, target.ID)
	if err != nil {
		return nil, nil, err
	}
	target = fresh
	if err := guardUpdate(ctx, h.d.DB, target, p); err != nil {
		if errors.Is(err, errSelf) || errors.Is(err, errLastAdmin) {
			field := "role"
			if p.Role == nil {
				field = "status"
			}
			return nil, map[string]string{field: guardKey(err)}, nil
		}
		return nil, nil, err
	}
	u, err := h.users.Update(ctx, target.ID, p)
	if err != nil {
		if f, ok := serviceFields(err); ok {
			return nil, f, nil
		}
		return nil, nil, err
	}
	if p.Role != nil && *p.Role != target.Role {
		h.d.Audit.Record(ctx, "user.role_change", "user", target.ID, map[string]string{"from": target.Role, "to": *p.Role})
	}
	return u, nil, nil
}

// deleteUser deletes targetID after the guards (under userMu, like
// updateUser). Guard errors are returned as errSelf / errLastAdmin.
func (h *handlers) deleteUser(ctx context.Context, targetID string) error {
	h.userMu.Lock()
	defer h.userMu.Unlock()
	if err := guardDelete(ctx, h.d.DB, h.users, targetID); err != nil {
		return err
	}
	return h.users.Delete(ctx, targetID)
}
