// Package admin serves the core admin screens: dashboard widgets, users,
// roles, general and mail settings, system health and jobs, API keys, the
// audit viewer and the global admin search. It also publishes the mailer
// service (mailer.ServiceName) and the REST operations for those resources.
package admin

import (
	"embed"
	"errors"
	"io/fs"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

//go:embed catalog/*.json
var catalogs embed.FS

//go:embed migrations/*.sql
var migrations embed.FS

// Permissions used by the admin screens.
const (
	PermUsers    = "users.manage"
	PermSettings = "settings.manage"
	PermHealth   = "system.health"
	PermAPIKeys  = "api_keys.manage"
	PermBackup   = "system.backup"
	PermUpdate   = "system.update"
)

// Module is the admin kernel.Module.
type Module struct{}

// New returns the admin module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "admin" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// handlers holds the admin screen dependencies.
type handlers struct {
	d       *kernel.Deps
	tpl     *httpx.Templates
	users   auth.Service
	roles   auth.RoleService
	keys    auth.KeyService
	mail    *mailer.Mailer
	aiLimit *aiLimiter // POST /admin/x/ai/write, per user
	started time.Time
	// userMu serialises user guard checks with the mutation they protect
	// (last-admin / self rules); GoRun is a single process.
	userMu            sync.Mutex
	updateMu          sync.Mutex
	inboundRateMu     sync.Mutex
	inboundRateWindow time.Time
	inboundRateCount  int

	canvasAIOnce        sync.Once
	canvasAISuggestions *canvasAICache
}

// Register declares permissions, publishes the mailer, adds nav items,
// dashboard widgets, admin routes and API operations.
func (*Module) Register(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("admin: admin templates service missing (auth module must boot first)")
	}
	users, ok := kernel.Get[auth.Service](d, kernel.ServiceAuth)
	if !ok {
		return errors.New("admin: auth service missing")
	}
	keys, ok := kernel.Get[auth.KeyService](d, kernel.ServiceAPIKeys)
	if !ok {
		return errors.New("admin: api key service missing")
	}
	roles, ok := kernel.Get[auth.RoleService](d, kernel.ServiceRoles)
	if !ok {
		return errors.New("admin: roles service missing")
	}
	if err := installManagedSourceTriggers(d.DB); err != nil {
		return err
	}
	declarePermissions(d)
	m := mailer.New(d.Settings, d.Log).WithKey(d.Config.SecretKey).WithEnv(d.Config.Env)
	d.Services.Set(mailer.ServiceName, m)

	h := &handlers{d: d, tpl: tpl, users: users, roles: roles, keys: keys, mail: m, aiLimit: newAILimiter(), started: time.Now()}
	tpl.SetSidebarBrandResolver(h.sidebarBrand)
	d.Services.Set(kernel.ServiceNavigationPins, kernel.NavigationPins(h))
	addNav(d)
	h.registerWorkspaceFilter()
	h.errorInventoryNav()
	h.registerWidgets()
	h.routes()
	h.studioRoutes()
	h.canvasMCPTools()
	h.canvasReadMCPTools()
	d.Services.Set(ServiceCanvasRead, CanvasReader(h))
	d.Jobs.Register("canvas.generate", h.canvasGenerateJob, 1)
	d.Jobs.Register("canvas.tools", h.managedToolsJob, 1)
	d.Jobs.Cron("canvas.schedule", time.Minute, h.canvasTick)
	d.Jobs.Register("studio.generate", h.studioGenerateJob, 1)
	d.Jobs.Cron("studio.recover", time.Minute, h.studioRecover)
	h.apiOperations()
	h.capabilityAPIOperations()
	h.roleAPIOperations()
	h.keyAPIOperations()
	h.canvasReadAPIOperations()
	d.Jobs.Cron("jobs.prune", time.Hour, h.pruneJobs)
	d.Jobs.Cron("backup.schedule", time.Hour, h.backupScheduleTick)
	return nil
}

func declarePermissions(d *kernel.Deps) {
	for _, p := range []struct{ name, en, ar string }{
		{"canvas.read", "Read selected canvas context", "قراءة سياق اللوحة المحدد"},
		{"canvas.append", "Append canvas results", "إضافة نتائج للوحة"},
		{"canvas.run", "Request canvas work", "طلب مهام اللوحة"},
		{PermUsers, "Manage users", "إدارة المستخدمين"},
		{PermSettings, "Manage settings", "إدارة الإعدادات"},
		{PermHealth, "View system health", "عرض حالة النظام"},
		{PermUpdate, "Deploy configured site updates", "نشر تحديثات الموقع المهيأة"},
		{PermAPIKeys, "Manage API keys", "إدارة مفاتيح API"},
		{PermBackup, "Make, download and restore backups", "إنشاء النسخ الاحتياطية وتنزيلها واستعادتها"},
	} {
		d.Perms.Declare(p.name, map[string]string{"en": p.en, "ar": p.ar})
	}
}

func addNav(d *kernel.Deps) {
	for _, n := range []kernel.NavItem{
		{ID: "canvases", LabelKey: "admin.canvas.title", Icon: "layout-dashboard", Href: "/admin/canvases", Position: 15},
		{ID: "agents.keys", LabelKey: "admin.nav.keys", Icon: "key-round", Href: "/admin/agents/keys", Permission: PermAPIKeys, Group: "agents", Position: 20},
		{ID: "agents.audit", LabelKey: "admin.nav.audit", Icon: "scroll-text", Href: "/admin/agents/audit", Permission: PermHealth, Group: "system", Position: 930},
		{ID: "storage", LabelKey: "admin.storage.title", Icon: "hard-drive", Href: "/admin/settings/storage", Permission: PermSettings, Group: "integrations", Position: 820},
		{ID: "users", LabelKey: "admin.nav.users", Icon: "users", Href: "/admin/users", Permission: PermUsers, Group: "settings", Position: 720},
		{ID: "roles", LabelKey: "admin.nav.roles", Icon: "shield", Href: "/admin/roles", Permission: PermUsers, Group: "settings", Position: 725},
		{ID: "settings.connections", LabelKey: "admin.connections.title", Icon: "plug", Href: "/admin/settings/connections", Permission: PermSettings, Group: "integrations", Position: 800},
		{ID: "settings", LabelKey: "admin.nav.settings", Icon: "settings", Href: "/admin/settings/general", Permission: PermSettings, Group: "settings", Position: 700},
		{ID: "settings.workspace", LabelKey: "admin.workspace.nav", Icon: "layout-template", Href: "/admin/workspace", Permission: PermSettings, Group: "settings", Position: 702},
		{ID: "settings.edge", LabelKey: "admin.settings.edge.nav", Icon: "cloud", Href: "/admin/settings/edge", Permission: PermSettings, Group: "settings", Position: 704},
		{ID: "system.updates", LabelKey: "admin.updates.title", Icon: "download", Href: "/admin/system/updates", Permission: PermUpdate, Group: "system", Position: 905},
		{ID: "system.health", LabelKey: "admin.nav.health", Icon: "activity", Href: "/admin/system/health", Permission: PermHealth, Group: "system", Position: 900},
		{ID: "system.jobs", LabelKey: "admin.nav.jobs", Icon: "list-checks", Href: "/admin/system/jobs", Permission: PermHealth, Group: "system", Position: 910},
		{ID: "system.backups", LabelKey: "admin.nav.backups", Icon: "archive", Href: "/admin/system/backups", Permission: PermBackup, Group: "system", Position: 920},
	} {
		d.Admin.AddNav(n)
	}
}
