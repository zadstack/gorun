package content

import (
	"context"
	"embed"
	"io/fs"
	"time"

	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Module is the content kernel.Module.
type Module struct {
	svc *service
}

// New returns the content module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "content" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// permissions declared by content (auth declares the same names; Declare
// merges labels).
var permissions = []struct{ name, en, ar string }{
	{"content.view", "View content", "عرض المحتوى"},
	{"content.create", "Create content", "إنشاء المحتوى"},
	{"content.edit", "Edit content", "تعديل المحتوى"},
	{"content.publish", "Publish content", "نشر المحتوى"},
	{"content.delete", "Delete content", "حذف المحتوى"},
	{"types.manage", "Manage content types", "إدارة أنواع المحتوى"},
	{PermModerateComments, "Moderate comments", "الإشراف على التعليقات"},
}

// Register publishes content.Service (kernel.ServiceContent), declares
// permissions, schedules the publish/purge crons and mounts the admin
// screens, API operations and the dashboard widget.
func (m *Module) Register(d *kernel.Deps) error {
	svc := newService(d.DB, d.Hooks, d.Audit, d.Settings, d.Log)
	// taxonomy registers after content: resolve the backend per call.
	svc.termsFn = func() TermBackend {
		tb, _ := kernel.Get[TermBackend](d, kernel.ServiceTaxonomy)
		return tb
	}
	m.svc = svc
	if err := svc.ensureSearch(context.Background()); err != nil {
		return err
	}
	d.Services.Set(kernel.ServiceContent, Service(svc))
	d.Services.Set(ServiceComments, CommentService(svc))
	for _, p := range permissions {
		d.Perms.Declare(p.name, map[string]string{"en": p.en, "ar": p.ar})
	}
	d.Jobs.Cron("content.publish_due", 30*time.Second, func(ctx context.Context) error { return svc.publishDue(ctx, time.Now()) })
	d.Jobs.Cron("content.purge_trash", time.Hour, func(ctx context.Context) error { return svc.purgeTrash(ctx, time.Now()) })
	if err := registerAdmin(d, svc); err != nil {
		return err
	}
	registerAPI(d, svc)
	return nil
}
