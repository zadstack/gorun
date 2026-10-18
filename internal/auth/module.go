// Package auth provides users, roles and permissions, sessions, CSRF, API
// keys, magic links, login rate limiting and the admin shell (login, setup,
// locale switch). The dashboard at /admin belongs to the admin module.
package auth

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/web"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the auth kernel.Module.
type Module struct{}

// New returns the auth module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "auth" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register declares permissions, publishes the services
// (kernel.ServiceAuth, ServiceAPIKeys, ServiceAdminTemplates), installs the
// auth middleware on Admin, API and Public, mounts the auth routes and
// finally calls Router.SetAuthenticator.
func (*Module) Register(d *kernel.Deps) error {
	if err := ReloadRoles(context.Background(), d.DB); err != nil {
		return err
	}
	for _, p := range basePermissions {
		d.Perms.Declare(p.name, map[string]string{"en": p.en, "ar": p.ar})
	}
	audit.ActorFromContext = auditActor
	d.Admin.SetPermissionCheck(Can)

	svc := newService(d.DB, d.Audit)
	d.Services.Set(kernel.ServiceAuth, Service(svc))
	d.Services.Set(kernel.ServiceAPIKeys, NewKeyService(d.DB, d.Audit, d.Perms))

	tpl, err := httpx.NewTemplates(web.FS, d.Config.Env != "prod", d.I18n, templateFuncs(d), d.Log)
	if err != nil {
		return fmt.Errorf("admin templates: %w", err)
	}
	d.Services.Set(kernel.ServiceAdminTemplates, tpl)
	renderErr := func(w http.ResponseWriter, r *http.Request, status int) { tpl.RenderError(w, r, status) }
	errorPage.Store(&renderErr)
	d.Admin.AddNav(kernel.NavItem{ID: "dashboard", LabelKey: "core.nav.dashboard", Icon: "layout-dashboard", Href: "/admin", Position: 0})

	h := &handlers{deps: d, svc: svc, sessions: newSessionStore(d.DB), tpl: tpl}
	// adminGate before CSRF: an expired-session HTMX POST gets HX-Redirect,
	// not a CSRF 403.
	d.Router.Admin.Use(h.securityMiddleware, SessionMiddleware(d), localeMiddleware, adminGate, CSRFMiddleware())
	d.Router.API.Use(h.securityMiddleware, APIKeyMiddleware(d))
	d.Router.Public.Use(h.securityMiddleware, SessionMiddleware(d))
	d.Router.Webhooks.Use(h.securityMiddleware)
	h.registerSecurity()

	d.Router.Admin.Get("/login", h.loginForm)
	d.Router.Admin.Post("/login", h.loginSubmit)
	d.Router.Admin.Post("/logout", h.logout)
	d.Router.Admin.Get("/setup", h.setupForm)
	d.Router.Admin.Post("/setup", h.setupSubmit)
	d.Router.Admin.Post("/locale", h.setLocale)
	d.Router.Admin.Get("/navigation", h.navigation)
	d.Router.Admin.Post("/navigation", h.navigationSave)
	d.Router.Admin.NotFound(func(w http.ResponseWriter, r *http.Request) { tpl.RenderError(w, r, http.StatusNotFound) })
	d.Router.Public.Get("/auth/magic/{token}", h.magicForm)
	d.Router.Public.Post("/auth/magic/{token}", h.magicSubmit) // CSRF-exempt: the single-use token is the credential
	d.Router.Public.Handle(httpx.StaticPrefix+"*", staticHandler())

	d.Jobs.Cron("auth.cleanup", time.Hour, func(ctx context.Context) error { return cleanup(ctx, d) })
	d.Router.SetAuthenticator("auth")
	return nil
}

func templateFuncs(d *kernel.Deps) httpx.Funcs {
	return httpx.Funcs{
		Nav: func(ctx context.Context) []httpx.NavItem {
			items := orderedNavigation(ctx, d)
			hidden, err := hiddenNavigation(ctx, d)
			if err != nil {
				d.Log.Error("auth: load navigation preferences", "err", err)
			}
			out := make([]httpx.NavItem, 0, len(items))
			for _, it := range items {
				if !hidden[it.ID] {
					out = append(out, httpx.NavItem(it))
				}
			}
			return out
		},
		AllNav: func(ctx context.Context) []httpx.NavItem {
			items := orderedNavigation(ctx, d)
			out := make([]httpx.NavItem, len(items))
			for i, it := range items {
				out[i] = httpx.NavItem(it)
			}
			return out
		},
		NavGroupOrder: func(ctx context.Context) []string {
			order, err := loadNavigationOrder(ctx, d)
			if err != nil {
				d.Log.Error("auth: load sidebar group order", "err", err)
				return nil
			}
			return order.Groups
		},
		User: func(ctx context.Context) any {
			if u := UserFromContext(ctx); u != nil {
				return u
			}
			return nil
		},
		CSRF:    CSRFToken,
		Setting: func(ctx context.Context, key string) string { return d.Settings.GetString(ctx, key, "") },
	}
}

// staticHandler serves web/admin/static; versioned URLs (?v=) are immutable.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(web.FS, "admin/static")
	files := http.StripPrefix(httpx.StaticPrefix, http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		// Public font bytes may be used by opaque-origin sandbox previews.
		name := strings.TrimPrefix(r.URL.Path, httpx.StaticPrefix)
		// ?v= URLs and version-named vendor files are immutable.
		w.Header().Set("Cache-Control", httpx.AssetCacheControl(r, name))
		if strings.HasPrefix(name, "fonts/") && strings.HasSuffix(name, ".woff2") {
			if st, err := fs.Stat(sub, name); err == nil && !st.IsDir() {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
		}
		files.ServeHTTP(w, r)
	})
}

// cleanup removes expired sessions, stale login attempts and old magic links.
func cleanup(ctx context.Context, d *kernel.Deps) error {
	now := db.Now()
	stale := db.FormatTime(time.Now().Add(-24 * time.Hour))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM sessions WHERE expires_at <= ?`, []any{now}},
		{`DELETE FROM auth_ip_blocks WHERE expires_at <= ?`, []any{now}},
		{`DELETE FROM login_attempts WHERE window_start <= ?`, []any{stale}},
		{`DELETE FROM magic_links WHERE expires_at <= ? OR used_at != ''`, []any{stale}},
	} {
		if _, err := d.DB.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("auth: cleanup: %w", err)
		}
	}
	return nil
}
