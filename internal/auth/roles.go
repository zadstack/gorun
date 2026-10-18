package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// Role errors returned by RoleService.
var (
	ErrRoleNotFound = errors.New("auth: role not found")
	ErrRoleInUse    = errors.New("auth: role is assigned to users")
	ErrSystemRole   = errors.New("auth: system role cannot be deleted or have its slug changed")
)

var roleSlugRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Role is a named permission pack. System roles are seeded; custom roles
// have System=false and may be deleted when no users reference them.
type Role struct {
	Slug        string
	Name        map[string]string // locale → label (en required)
	System      bool
	Permissions []string
	UserCount   int
}

// RolePatch updates only its non-nil fields. Permissions replaces the full
// grant set when non-nil (empty slice clears all grants).
type RolePatch struct {
	Name        map[string]string
	Permissions *[]string
}

// RoleService manages roles and role_permissions.
type RoleService interface {
	List(ctx context.Context) ([]Role, error)
	Get(ctx context.Context, slug string) (*Role, error)
	Create(ctx context.Context, slug string, name map[string]string, permissions []string) (*Role, error)
	Update(ctx context.Context, slug string, patch RolePatch) (*Role, error)
	Delete(ctx context.Context, slug string) error
}

type roleService struct {
	db    *sql.DB
	audit *audit.Writer
	perms *kernel.PermissionRegistry
}

// NewRoleService returns the role service. Create/Update validate
// permissions against perms (nil skips the known-permission check).
func NewRoleService(d *sql.DB, a *audit.Writer, perms *kernel.PermissionRegistry) RoleService {
	return &roleService{db: d, audit: a, perms: perms}
}

func (s *roleService) record(ctx context.Context, action, slug string, meta any) {
	if s.audit != nil {
		s.audit.Record(ctx, action, "role", slug, meta)
	}
}

func (s *roleService) List(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug, r.name_json, r.system,
			(SELECT count(*) FROM role_permissions WHERE role = r.slug),
			(SELECT count(*) FROM users WHERE role = r.slug)
		FROM roles r
		ORDER BY r.system DESC, r.slug`)
	if err != nil {
		return nil, fmt.Errorf("auth: list roles: %w", err)
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		r, err := scanRoleSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		perms, err := s.loadPerms(ctx, out[i].Slug)
		if err != nil {
			return nil, err
		}
		out[i].Permissions = perms
	}
	return out, nil
}

func (s *roleService) Get(ctx context.Context, slug string) (*Role, error) {
	slug = strings.TrimSpace(slug)
	var raw string
	var system int
	var users int
	err := s.db.QueryRowContext(ctx, `
		SELECT name_json, system, (SELECT count(*) FROM users WHERE role = ?)
		FROM roles WHERE slug = ?`, slug, slug).Scan(&raw, &system, &users)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: get role: %w", err)
	}
	perms, err := s.loadPerms(ctx, slug)
	if err != nil {
		return nil, err
	}
	return &Role{
		Slug:        slug,
		Name:        decodeNameJSON(raw),
		System:      system != 0,
		Permissions: perms,
		UserCount:   users,
	}, nil
}

func (s *roleService) Create(ctx context.Context, slug string, name map[string]string, permissions []string) (*Role, error) {
	slug = strings.TrimSpace(slug)
	name = normalizeRoleName(name)
	permissions = normalizePerms(permissions)
	if fields := s.validateNew(slug, name, permissions); len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	raw, err := json.Marshal(name)
	if err != nil {
		return nil, fmt.Errorf("auth: encode role name: %w", err)
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO roles (slug, name_json, system) VALUES (?, ?, 0)`, slug, string(raw)); err != nil {
			if isUniqueViolation(err) {
				return &ValidationError{Fields: map[string]string{"slug": "auth.error.role_slug_taken"}}
			}
			return fmt.Errorf("auth: insert role: %w", err)
		}
		return insertRolePerms(ctx, tx, slug, permissions)
	})
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return nil, ve
		}
		return nil, err
	}
	if err := ReloadRoles(ctx, s.db); err != nil {
		return nil, err
	}
	s.record(ctx, "role.create", slug, map[string]any{"permissions": permissions})
	return s.Get(ctx, slug)
}

func (s *roleService) Update(ctx context.Context, slug string, patch RolePatch) (*Role, error) {
	slug = strings.TrimSpace(slug)
	cur, err := s.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	name := cur.Name
	if patch.Name != nil {
		name = normalizeRoleName(patch.Name)
	}
	perms := cur.Permissions
	permsChanged := false
	if patch.Permissions != nil {
		perms = normalizePerms(*patch.Permissions)
		permsChanged = true
	}
	fields := map[string]string{}
	if patch.Name != nil {
		if f := validateRoleName(name); len(f) > 0 {
			for k, v := range f {
				fields[k] = v
			}
		}
	}
	if permsChanged {
		if cur.Slug == RoleAdmin {
			if !slices.Equal(perms, []string{"*"}) {
				fields["permissions"] = "auth.error.role_admin_locked"
			}
		} else if f := s.validatePerms(perms); f != "" {
			fields["permissions"] = f
		}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	if patch.Name == nil && !permsChanged {
		return cur, nil
	}
	raw, err := json.Marshal(name)
	if err != nil {
		return nil, fmt.Errorf("auth: encode role name: %w", err)
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if patch.Name != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE roles SET name_json = ? WHERE slug = ?`, string(raw), slug); err != nil {
				return fmt.Errorf("auth: update role name: %w", err)
			}
		}
		if permsChanged {
			if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role = ?`, slug); err != nil {
				return fmt.Errorf("auth: clear role permissions: %w", err)
			}
			if err := insertRolePerms(ctx, tx, slug, perms); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if permsChanged {
		if err := ReloadRoles(ctx, s.db); err != nil {
			return nil, err
		}
	}
	meta := map[string]any{}
	if patch.Name != nil {
		meta["name"] = name
	}
	if permsChanged {
		meta["permissions"] = perms
	}
	s.record(ctx, "role.update", slug, meta)
	return s.Get(ctx, slug)
}

func (s *roleService) Delete(ctx context.Context, slug string) error {
	slug = strings.TrimSpace(slug)
	cur, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	if cur.System {
		return ErrSystemRole
	}
	if cur.UserCount > 0 {
		return ErrRoleInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM roles WHERE slug = ? AND system = 0`, slug)
	if err != nil {
		return fmt.Errorf("auth: delete role: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRoleNotFound
	}
	if err := ReloadRoles(ctx, s.db); err != nil {
		return err
	}
	s.record(ctx, "role.delete", slug, nil)
	return nil
}

func (s *roleService) loadPerms(ctx context.Context, slug string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT permission FROM role_permissions WHERE role = ? ORDER BY permission`, slug)
	if err != nil {
		return nil, fmt.Errorf("auth: list role permissions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("auth: scan role permission: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *roleService) validateNew(slug string, name map[string]string, permissions []string) map[string]string {
	fields := map[string]string{}
	if !roleSlugRE.MatchString(slug) {
		fields["slug"] = "auth.error.role_slug_invalid"
	}
	for k, v := range validateRoleName(name) {
		fields[k] = v
	}
	if f := s.validatePerms(permissions); f != "" {
		fields["permissions"] = f
	}
	return fields
}

func (s *roleService) validatePerms(permissions []string) string {
	for _, p := range permissions {
		if p == "*" {
			return "auth.error.role_wildcard_forbidden"
		}
	}
	if bad := s.unknownPerms(permissions); len(bad) > 0 {
		return "auth.error.scope_invalid"
	}
	return ""
}

func (s *roleService) unknownPerms(permissions []string) []string {
	if s.perms == nil {
		return nil
	}
	known := map[string]bool{}
	for _, p := range s.perms.All() {
		known[p.Name] = true
	}
	var bad []string
	for _, p := range permissions {
		if !known[p] {
			bad = append(bad, p)
		}
	}
	return bad
}

func validateRoleName(name map[string]string) map[string]string {
	fields := map[string]string{}
	en := strings.TrimSpace(name["en"])
	if en == "" {
		fields["name_en"] = "auth.error.role_name_required"
	} else if len([]rune(en)) > 80 {
		fields["name_en"] = "auth.error.role_name_long"
	}
	ar := strings.TrimSpace(name["ar"])
	if ar == "" {
		fields["name_ar"] = "auth.error.role_name_required"
	} else if len([]rune(ar)) > 80 {
		fields["name_ar"] = "auth.error.role_name_long"
	}
	return fields
}

func normalizeRoleName(name map[string]string) map[string]string {
	out := map[string]string{}
	for _, loc := range []string{"en", "ar"} {
		if v := strings.TrimSpace(name[loc]); v != "" {
			out[loc] = v
		}
	}
	return out
}

func normalizePerms(perms []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func insertRolePerms(ctx context.Context, tx *sql.Tx, slug string, permissions []string) error {
	for _, p := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role, permission) VALUES (?, ?)`, slug, p); err != nil {
			return fmt.Errorf("auth: insert role permission: %w", err)
		}
	}
	return nil
}

func decodeNameJSON(raw string) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

type roleSummaryScanner interface {
	Scan(dest ...any) error
}

func scanRoleSummary(row roleSummaryScanner) (Role, error) {
	var slug, raw string
	var system, permCount, userCount int
	if err := row.Scan(&slug, &raw, &system, &permCount, &userCount); err != nil {
		return Role{}, fmt.Errorf("auth: scan role: %w", err)
	}
	return Role{
		Slug:      slug,
		Name:      decodeNameJSON(raw),
		System:    system != 0,
		UserCount: userCount,
	}, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint failed")
}
