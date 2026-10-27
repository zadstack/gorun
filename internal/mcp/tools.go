package mcp

import (
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// Scopes used by the core tools.
const (
	scopeView     = "content.view"
	scopeCreate   = "content.create"
	scopeEdit     = "content.edit"
	scopePublish  = "content.publish"
	scopeDelete   = "content.delete"
	scopeTypes    = "types.manage"
	scopeUpload   = "media.upload"
	scopeTaxonomy = "taxonomy.manage"
	scopeThemes   = "themes.manage"
	scopeAudit    = "audit.read"
	scopeSettings = "settings.manage"
	scopeMigrate  = "migrations.run"
	scopeCommerce = "commerce.manage"
)

// pageOut is the paginated list shape returned by list tools.
type pageOut[T any] struct {
	Items   []T `json:"items"`
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

func newPage[T any](items []T, total, page, perPage int) pageOut[T] {
	if items == nil {
		items = []T{}
	}
	return pageOut[T]{Items: items, Total: total, Page: page, PerPage: perPage}
}

// paging normalises page/per_page (defaults 1/20, max 100).
func paging(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage <= 0:
		perPage = 20
	case perPage > 100:
		perPage = 100
	}
	return page, perPage
}

// registerCoreTools adds the core tool set to Deps.MCP. Tools whose
// service is absent (optional modules, trimmed test boots) are skipped.
func registerCoreTools(d *kernel.Deps, s *server) {
	tools := []kernel.MCPTool{s.checkApprovalTool()}
	if svc, ok := kernel.Get[content.Service](d, kernel.ServiceContent); ok {
		tools = append(tools, typeTools(svc)...)
		tools = append(tools, contentTools(svc)...)
		s.dynamic["update_content"] = liveItemRule(svc)
		s.dynamic["link_translation"] = liveGroupRule(svc, "id", "translation_id")
		s.dynamic["unlink_translation"] = liveGroupRule(svc, "id")
		tools = append(tools, s.siteTool())
	}
	if svc, ok := kernel.Get[content.CommentService](d, content.ServiceComments); ok {
		tools = append(tools, commentTools(svc)...)
		s.dynamic["moderate_comment"] = approvingComment
	}
	if svc, ok := kernel.Get[media.Service](d, kernel.ServiceMedia); ok {
		tools = append(tools, mediaTools(svc, s.log)...)
	}
	if svc, ok := kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy); ok {
		tools = append(tools, taxonomyTools(svc)...)
	}
	if svc, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme); ok {
		tools = append(tools, templateTools(svc)...)
	}
	tools = append(tools, logTools(d), settingsTool(d))
	tools = append(tools, migrationTools(d, s)...)
	tools = append(tools, commerceTools(d, s)...)
	for _, t := range tools {
		d.MCP.AddTool(t)
	}
}

func (s *server) checkApprovalTool() kernel.MCPTool {
	return kernel.MCPTool{Name: "check_approval", Input: checkApprovalInput{},
		Description: "Status of a sensitive call awaiting human approval: pending_approval, approved (with the stored result), denied or expired.",
		Handler:     bind(s.checkApproval)}
}
