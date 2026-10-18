package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
)

// Errors returned by the user guards.
var (
	errSelf      = errors.New("admin: you cannot delete, demote or disable your own account")
	errLastAdmin = errors.New("admin: at least one active administrator must remain")
)

// guardKey maps a guard error to its i18n key.
func guardKey(err error) string {
	if errors.Is(err, errSelf) {
		return "admin.users.error_self"
	}
	return "admin.users.error_last_admin"
}

func isActiveAdmin(u *auth.User) bool {
	return u != nil && u.Role == auth.RoleAdmin && u.Status == auth.StatusActive
}

// activeAdmins counts active administrators. auth.Service has no role
// filter, so this reads the users table directly (read-only).
func activeAdmins(ctx context.Context, d *sql.DB) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = ? AND status = ?`, auth.RoleAdmin, auth.StatusActive).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("admin: count admins: %w", err)
	}
	return n, nil
}

// guardDelete refuses deleting yourself or the last active admin.
func guardDelete(ctx context.Context, d *sql.DB, users auth.Service, targetID string) error {
	if actor := auth.UserFromContext(ctx); actor != nil && actor.ID == targetID {
		return errSelf
	}
	target, err := users.Get(ctx, targetID)
	if err != nil {
		return err
	}
	return guardAdminRemoval(ctx, d, target)
}

// guardUpdate refuses changing your own role or disabling yourself, and any
// change that would leave no active admin.
func guardUpdate(ctx context.Context, d *sql.DB, target *auth.User, p auth.UserPatch) error {
	roleChange := p.Role != nil && *p.Role != target.Role
	disables := p.Status != nil && *p.Status != auth.StatusActive && target.Status == auth.StatusActive
	if !roleChange && !disables {
		return nil
	}
	if actor := auth.UserFromContext(ctx); actor != nil && actor.ID == target.ID {
		return errSelf
	}
	if roleChange && *p.Role == auth.RoleAdmin && !disables {
		return nil
	}
	return guardAdminRemoval(ctx, d, target)
}

func guardAdminRemoval(ctx context.Context, d *sql.DB, target *auth.User) error {
	if !isActiveAdmin(target) {
		return nil
	}
	n, err := activeAdmins(ctx, d)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastAdmin
	}
	return nil
}

// roleOption is a role for select inputs.
type roleOption struct{ Slug, Name string }

// roles lists roles with names in locale for user forms. Full role CRUD
// lives on auth.RoleService and /admin/roles + /api/v1/roles.
func roles(ctx context.Context, d *sql.DB, locale string) ([]roleOption, error) {
	rows, err := d.QueryContext(ctx, `SELECT slug, name_json FROM roles ORDER BY system DESC, slug`)
	if err != nil {
		return nil, fmt.Errorf("admin: list roles: %w", err)
	}
	defer rows.Close()
	var out []roleOption
	for rows.Next() {
		var slug, raw string
		if err := rows.Scan(&slug, &raw); err != nil {
			return nil, fmt.Errorf("admin: scan role: %w", err)
		}
		names := map[string]string{}
		_ = json.Unmarshal([]byte(raw), &names)
		name := names[locale]
		if name == "" {
			name = names["en"]
		}
		if name == "" {
			name = slug
		}
		out = append(out, roleOption{Slug: slug, Name: name})
	}
	return out, rows.Err()
}

func validLocale(l string) bool { return slices.Contains(i18n.Supported, l) }

// userQuery filters listUsers.
type userQuery struct {
	Search, Role  string
	Page, PerPage int
}

const userColumns = `id, email, name, role, locale, status, last_login_at, created_at, updated_at`

// listUsers is auth.Service.List plus a role filter (read-only SQL).
func listUsers(ctx context.Context, d *sql.DB, q userQuery) ([]auth.User, int, error) {
	var conds []string
	var args []any
	if q.Search != "" {
		like := "%" + likeEscape(q.Search) + "%"
		conds = append(conds, `(email LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	if q.Role != "" {
		conds = append(conds, `role = ?`)
		args = append(args, q.Role)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count users: %w", err)
	}
	rows, err := d.QueryContext(ctx, `SELECT `+userColumns+` FROM users`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("admin: list users: %w", err)
	}
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		var u auth.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &u.Status, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("admin: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
