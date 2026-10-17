// Package mcp is GoRun's official Model Context Protocol server: the
// streamable HTTP endpoint at /mcp (bearer API keys only) and the stdio
// transport behind `gorun mcp --key`. Tools are the core set registered
// here plus every kernel.MCPTool other modules add to Deps.MCP. Each call is
// scope-checked with auth.Can, audited (mcp.call) and, for sensitive tools,
// parked in mcp_approvals until a human approves it in
// "AI Agents → Approvals" (unless the key holds mcp.approve_self).
package mcp

import (
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed catalog/*.json
var catalogs embed.FS

// Permissions declared by this module.
const (
	// PermApprove lets a user decide pending sensitive tool calls.
	PermApprove = "mcp.approve"
	// PermApproveSelf is a key scope: sensitive tools run without approval.
	PermApproveSelf = "mcp.approve_self"
)

const (
	approvalTTL      = 24 * time.Hour
	expireEvery      = time.Hour
	serverName       = "gorun"
	serverVersion    = "1.1.0"
	cronExpire       = "mcp.approvals.expire"
	serviceWPMigrate = "wpmigrate"
)

// Module is the mcp kernel.Module.
type Module struct {
	srv *server
}

// New returns the mcp module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return "mcp" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return migrations }

// Catalogs implements kernel.CatalogProvider.
func (*Module) Catalogs() fs.FS { return catalogs }

// Register declares the permissions, registers the core tools into
// Deps.MCP, mounts /mcp, the approvals admin page and API operations, the
// expiry cron and the health check.
func (m *Module) Register(d *kernel.Deps) error {
	d.Perms.Declare(PermApprove, map[string]string{"en": "Approve AI agent actions", "ar": "الموافقة على إجراءات وكلاء الذكاء الاصطناعي"})
	d.Perms.Declare(PermApproveSelf, map[string]string{"en": "Run sensitive MCP tools without approval (API key scope)", "ar": "تشغيل أدوات MCP الحساسة دون موافقة (نطاق مفتاح API)"})

	keys, ok := kernel.Get[auth.KeyService](d, kernel.ServiceAPIKeys)
	if !ok {
		return errors.New("mcp: api key service missing (auth module must boot first)")
	}
	users, ok := kernel.Get[auth.Service](d, kernel.ServiceAuth)
	if !ok {
		return errors.New("mcp: auth service missing")
	}
	s := newServer(d, keys, users)
	m.srv = s
	registerCoreTools(d, s)

	d.Router.Public.Handle("/mcp", s.httpHandler())
	if tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates); ok {
		s.registerAdmin(tpl)
	}
	s.registerAPI()
	d.Jobs.Cron(cronExpire, expireEvery, s.approvals.expire)
	d.Hooks.Filter(admin.HealthCheckFilter, 50, s.healthCheck)
	return nil
}
