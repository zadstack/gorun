package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zadstack/gorun/internal/httpx"
)

// basePermissions are declared by auth (spec §3); other modules declare
// their own. Labels: en, ar.
var basePermissions = []struct{ name, en, ar string }{
	{"content.view", "View content", "عرض المحتوى"},
	{"content.create", "Create content", "إنشاء المحتوى"},
	{"content.edit", "Edit content", "تعديل المحتوى"},
	{"content.publish", "Publish content", "نشر المحتوى"},
	{"content.delete", "Delete content", "حذف المحتوى"},
	{"types.manage", "Manage content types", "إدارة أنواع المحتوى"},
	{"media.upload", "Upload media", "رفع الوسائط"},
	{"media.delete", "Delete media", "حذف الوسائط"},
	{"taxonomy.manage", "Manage taxonomies", "إدارة التصنيفات"},
	{"menus.manage", "Manage menus", "إدارة القوائم"},
	{"users.manage", "Manage users", "إدارة المستخدمين"},
	{"settings.manage", "Manage settings", "إدارة الإعدادات"},
	{"themes.manage", "Manage themes", "إدارة القوالب"},
	{"extensions.manage", "Manage extensions", "إدارة الإضافات"},
	{"migrations.run", "Run migrations", "تشغيل عمليات الترحيل"},
	{"commerce.manage", "Manage the store", "إدارة المتجر"},
	{"commerce.view_orders", "View orders", "عرض الطلبات"},
	{"mcp.approve", "Approve AI agent actions", "الموافقة على إجراءات وكلاء الذكاء الاصطناعي"},
	{"security.manage", "Manage login security", "إدارة أمان تسجيل الدخول"},
	{"system.health", "View system health", "عرض حالة النظام"},
}

// roleCache is the role → permission mapping used by Can. It is loaded by
// the module's Register and must be reloaded (ReloadRoles) after role edits.
var roleCache = struct {
	mu    sync.RWMutex
	perms map[string]map[string]bool
}{perms: map[string]map[string]bool{}}

// ReloadRoles reloads the role → permission mapping from role_permissions.
func ReloadRoles(ctx context.Context, d *sql.DB) error {
	rows, err := d.QueryContext(ctx, `SELECT role, permission FROM role_permissions`)
	if err != nil {
		return fmt.Errorf("auth: load roles: %w", err)
	}
	defer rows.Close()
	perms := map[string]map[string]bool{}
	for rows.Next() {
		var role, perm string
		if err := rows.Scan(&role, &perm); err != nil {
			return fmt.Errorf("auth: scan role: %w", err)
		}
		if perms[role] == nil {
			perms[role] = map[string]bool{}
		}
		perms[role][perm] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	roleCache.mu.Lock()
	roleCache.perms = perms
	roleCache.mu.Unlock()
	return nil
}

func roleAllows(role, perm string) bool {
	roleCache.mu.RLock()
	defer roleCache.mu.RUnlock()
	p := roleCache.perms[role]
	return p["*"] || p[perm]
}

// scopeAllows reports whether API key scopes grant perm ("*" = all).
func scopeAllows(scopes []string, perm string) bool {
	return slices.Contains(scopes, "*") || slices.Contains(scopes, perm)
}

// Can reports whether the principal in ctx holds perm: an active user whose
// role grants it, and for API keys additionally a matching scope (a key
// never exceeds its owner's role).
func Can(ctx context.Context, perm string) bool {
	p := principalFrom(ctx)
	if p == nil || p.user == nil || p.user.Status != StatusActive {
		return false
	}
	if !roleAllows(p.user.Role, perm) {
		return false
	}
	if p.kind == "api_key" {
		return scopeAllows(p.scopes, perm)
	}
	return true
}

// RequirePerm answers 403 unless Can(ctx, perm): a JSON envelope for API
// requests, plain text otherwise.
func RequirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Can(r.Context(), perm) {
				deny(w, r, http.StatusForbidden, "forbidden", "missing permission")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// errorPage renders a browser error page (pages/errors/<status>.html); the
// module sets it in Register. Without it deny falls back to plain text.
var errorPage atomic.Pointer[func(w http.ResponseWriter, r *http.Request, status int)]

// deny answers an auth failure: the JSON envelope for API and HTMX
// requests, the rendered error page for browsers.
func deny(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if isAPIRequest(r) || r.Header.Get("HX-Request") == "true" {
		httpx.Error(w, status, code, msg, nil)
		return
	}
	if render := errorPage.Load(); render != nil {
		(*render)(w, r, status)
		return
	}
	http.Error(w, http.StatusText(status), status)
}

// CanGrant reports whether the principal in ctx may hand out every scope in
// scopes (when creating or rotating an API key): each permission must pass
// Can, and "*" needs a role that holds "*" (and, for a key, "*" in the key's
// own scopes). Nobody can therefore mint a key stronger than themselves.
func CanGrant(ctx context.Context, scopes []string) bool {
	p := principalFrom(ctx)
	if p == nil || p.user == nil {
		return false
	}
	for _, s := range scopes {
		if s == "*" {
			if p.user.Status != StatusActive || !roleAllows(p.user.Role, "*") || (p.kind == "api_key" && !scopeAllows(p.scopes, "*")) {
				return false
			}
			continue
		}
		if !Can(ctx, s) {
			return false
		}
	}
	return true
}
