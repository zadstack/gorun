// Package ext is the extension system (spec §4.9): extensions are
// external programs described by a manifest. Installing one validates the
// manifest, registers its content types and settings and issues a scoped
// API key; activating it subscribes its declared hooks, which the HTTP
// bridge delivers to the extension's endpoint as HMAC-signed JSON. There
// are no Go native plugins: internal modules and extensions use the same
// hooks registry, the bridge is just another subscriber. See
// docs/EXTENSIONS.md.
package ext

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// ServiceName is the kernel service name of ext.Service.
const ServiceName = "ext"

// Perm is the permission for managing extensions.
const Perm = "extensions.manage"

// Module is the ext kernel.Module.
type Module struct{}

// New returns the ext module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "ext" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

type service struct {
	db       *sql.DB
	hooks    *hooks.Registry
	jobs     *jobs.Queue
	settings *settings.Store
	audit    *audit.Writer
	log      *slog.Logger
	i18n     *i18n.Bundle
	cfg      config.Config
	perms    *kernel.PermissionRegistry
	keys     auth.KeyService
	content  func() content.Service // nil result when the content module is not booted
	client   *http.Client
	now      func() time.Time
	mu       sync.Mutex // serialises lifecycle operations
	rt       *bridge
}

func (*Module) Enabled(ctx context.Context, database *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, database, "ext")
}

// Register declares the permission, publishes the service, resubscribes
// active extensions, and adds the delivery job, health check, admin screens
// and API operations.
func (*Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(Perm, map[string]string{"en": "Manage extensions", "ar": "إدارة الإضافات"})
	keys, ok := kernel.Get[auth.KeyService](d, kernel.ServiceAPIKeys)
	if !ok {
		return errors.New("ext: api key service missing (auth module must boot first)")
	}
	s := &service{
		db: d.DB, hooks: d.Hooks, jobs: d.Jobs, settings: d.Settings, audit: d.Audit, log: d.Log, i18n: d.I18n, cfg: d.Config,
		perms: d.Perms, keys: keys, now: time.Now, rt: newBridge(),
		content: func() content.Service {
			cs, _ := kernel.Get[content.Service](d, kernel.ServiceContent)
			return cs
		},
		client: httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: d.Config.AllowPrivateNet, Timeout: asyncTimeout + 5*time.Second}),
	}
	d.Jobs.Register(DeliverJobKind, s.handleDeliver, deliverMaxAttempts)
	if err := s.resubscribe(context.Background()); err != nil {
		return err
	}
	d.Services.Set(ServiceName, Service(s))
	d.Hooks.Filter(admin.HealthCheckFilter, 100, s.healthCheck)
	d.Hooks.Filter(admin.ModuleCatalogFilter, 100, s.catalogEntries)
	if err := registerAdmin(d, s); err != nil {
		return err
	}
	registerAPI(d, s)
	return nil
}

// resubscribe restores delivery for every active extension at boot.
func (s *service) resubscribe(ctx context.Context) error {
	list, err := s.list(ctx, `WHERE status = ?`, StatusActive)
	if err != nil {
		return err
	}
	for i := range list {
		x := &list[i]
		secret, err := db.Decrypt(s.cfg.SecretKey, x.secretEnc, "ext:"+x.Slug)
		if err != nil {
			s.log.Error("ext: cannot decrypt secret, marking extension as error", "ext", x.Slug, "err", err)
			_ = s.setStatus(ctx, x.Slug, StatusError, `, last_error = ?`, "secret could not be decrypted (secret key changed?)")
			continue
		}
		s.rt.activate(s, x, secret)
	}
	return nil
}

// healthCheck adds the "Extensions" line to System Health.
func (s *service) healthCheck(ctx context.Context, v any) (any, error) {
	checks, ok := v.(*[]admin.HealthCheck)
	if !ok {
		return v, nil
	}
	tr := func(key string, args ...any) string {
		if s.i18n == nil {
			return key
		}
		return s.i18n.T(i18n.Locale(ctx), key, args...)
	}
	list, err := s.list(ctx, `WHERE status = ?`, StatusError)
	c := admin.HealthCheck{Name: "extensions", Status: admin.CheckOK, Detail: tr("ext.health.ok")}
	switch {
	case err != nil:
		c.Status, c.Detail = admin.CheckWarn, tr("ext.health.unreadable")
	case len(list) > 0:
		slugs := make([]string, 0, len(list))
		for _, x := range list {
			slugs = append(slugs, x.Slug)
		}
		c.Status, c.Detail = admin.CheckFail, tr("ext.health.error", len(list), strings.Join(slugs, ", "))
	}
	*checks = append(*checks, c)
	return checks, nil
}

// catalogEntries adds one System → Modules entry per installed extension:
// what the manifest says it does, its version (compatibility is not
// verified), the permissions its key carries, the hooks it subscribes to,
// and whether it declares settings (needs configuration) or is in error.
func (s *service) catalogEntries(ctx context.Context, v any) (any, error) {
	list, ok := v.(*[]admin.CatalogEntry)
	if !ok || list == nil {
		return v, nil
	}
	items, err := s.list(ctx, ``)
	if err != nil {
		return v, err
	}
	for _, x := range items {
		hooks := make([]string, 0, len(x.Manifest.Hooks))
		for _, h := range x.Manifest.Hooks {
			hooks = append(hooks, h.Name)
		}
		name := map[string]string{"en": x.DisplayName("en"), "ar": x.DisplayName("ar")}
		summary := map[string]string{"en": x.Manifest.Description["en"], "ar": x.Manifest.Description["ar"]}
		for _, loc := range []string{"en", "ar"} {
			if summary[loc] == "" {
				summary[loc] = s.i18n.T(loc, "ext.catalog.no_description")
			}
		}
		e := admin.CatalogEntry{ID: "ext:" + x.Slug, Kind: admin.CatalogKindExtension, Source: admin.CatalogSourceExternal, Name: name, Summary: summary,
			Version: x.Version, Compatibility: admin.CompatManifestUnverified, Owner: "ext",
			Dependencies: []string{"ext"}, OptionalDependencies: []string{}, Dependents: []string{}, Permissions: append([]string{}, x.Manifest.Permissions...),
			Registered: &kernel.ModuleRegistration{HookSubscriptions: hooks, Permissions: append([]string{}, x.Manifest.Permissions...), APIOperations: []string{}, MCPTools: []string{}, JobKinds: []string{}, Crons: []string{}, Nav: []string{}, Widgets: []string{}, ThemeFuncs: []string{}, ThemeKinds: []string{}, ServicesPublished: []string{}, ServicesConsumed: []string{}},
			Jobs:       admin.CatalogJobs{Kinds: []string{}}, Messages: []admin.CatalogMessage{},
			Links: map[string]string{"extension": "/admin/extensions/" + x.Slug},
			State: admin.CatalogState{Available: true, Optional: true, Switchable: false, Status: x.Status, Enabled: x.Status == StatusActive}}
		if x.Status == StatusActive {
			e.Jobs.Kinds = []string{DeliverJobKind}
		}
		if len(x.Manifest.Settings) > 0 {
			e.Configuration = admin.CatalogConfiguration{Status: "configured", Reason: "settings_declared", Remediation: "Review the extension's settings on its page."}
			for _, f := range x.Manifest.Settings {
				if s.settings.GetString(ctx, "ext."+x.Slug+".settings."+f.Key, "") == "" {
					e.Configuration = admin.CatalogConfiguration{Status: "incomplete", Reason: "setting_empty:" + f.Key, Remediation: "Fill in the extension's settings on its page."}
					break
				}
			}
		} else {
			e.Configuration = admin.CatalogConfiguration{Status: "not_required", Reason: "no_settings_declared"}
		}
		if x.Status == StatusError {
			e.Messages = append(e.Messages, admin.CatalogMessage{Level: "warning", Text: map[string]string{"en": s.i18n.T("en", "ext.catalog.error", x.LastError), "ar": s.i18n.T("ar", "ext.catalog.error", x.LastError)}})
		}
		*list = append(*list, e)
	}
	return list, nil
}
