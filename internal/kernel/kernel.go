// Package kernel defines the Module contract and boots GoRun: it opens the
// database, runs each module's migrations, lets modules register into shared
// registries, and assembles the HTTP handler.
package kernel

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// Module is a pluggable domain. Migrations returns an FS whose files live at
// "migrations/NNNN_name.sql" (nil when the module has no tables). Register
// wires the module into Deps; it runs once, in BootOrder.
type Module interface {
	Name() string
	Migrations() fs.FS
	Register(d *Deps) error
}

// CatalogProvider is optionally implemented by modules that ship
// translations. The FS holds "catalog/<module>.<locale>.json" files; they are
// loaded before any non-settings module registers.
type CatalogProvider interface {
	Catalogs() fs.FS
}

// Deps is the set of shared services handed to every module's Register.
type Deps struct {
	DB       *sql.DB
	Hooks    *hooks.Registry
	Jobs     *jobs.Queue
	Settings *settings.Store
	I18n     *i18n.Bundle // nil only while the settings module registers
	Log      *slog.Logger
	Config   config.Config
	Router   *Router
	Admin    *AdminRegistry
	Theme    *ThemeRegistry
	API      *APIRegistry
	MCP      *MCPRegistry
	Perms    *PermissionRegistry
	Audit    *audit.Writer
	Services *ServiceRegistry // Set(name, v) / Get[T](d, name): reach earlier modules' services without import cycles
}

// Router holds the chi sub-routers modules register on during Register.
// Use may be called on any of them at any point during Register, even after
// earlier modules added routes: middlewares are collected and wrap every
// route of that router, in Use order, from the first request on (Use after
// serving starts panics). Boot mounts the routers on the root mux after
// every module has registered.
//
// Mount prefixes: Public "/", Admin "/admin", API "/api" (modules add
// "/v1/..."; the spec's "/api/docs" sits outside v1), Webhooks "/webhooks".
// Session/CSRF/permission middleware on Admin and key-or-session on API are
// installed by the auth module via Admin.Use / API.Use; Webhooks stays raw.
// Admin and API deny every request (503) until SetAuthenticator is called.
// The root mux also serves GET /healthz (liveness), GET /readyz (DB ping),
// and optionally GET /metrics (Prometheus text when GORUN_METRICS is on),
// outside every sub-router.
type Router struct {
	Public, Admin, API, Webhooks chi.Router

	authenticator atomic.Pointer[string]
}

// SetAuthenticator records that a module (the auth module, in its Register)
// has installed authentication middleware on Admin and API. Until it is
// called, Boot's guard answers every Admin and API request with 503
// "authentication module not installed" (deny by default).
func (r *Router) SetAuthenticator(name string) { r.authenticator.Store(&name) }

// Authenticator returns the name passed to SetAuthenticator, or "".
func (r *Router) Authenticator() string {
	if p := r.authenticator.Load(); p != nil {
		return *p
	}
	return ""
}

// App is a booted GoRun instance. Close stops the job workers, waits for
// them, then closes the database; it is safe to call more than once.
type App struct {
	Deps    *Deps
	Handler http.Handler
	Close   func()
}
