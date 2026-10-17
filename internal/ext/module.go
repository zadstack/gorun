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
