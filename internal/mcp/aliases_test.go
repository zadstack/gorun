package mcp

import (
	"testing"
)

// deprecatedAliases maps every renamed tool's old name to its current name.
var deprecatedAliases = map[string]string{
	"canvases.pending":       "list_canvas_pending_runs",
	"canvases.context":       "get_canvas_context",
	"canvases.source":        "get_canvas_source",
	"canvases.request_run":   "request_canvas_run",
	"canvases.append_result": "append_canvas_result",
	"channels_list":          "list_channels",
	"channels_read":          "read_channel_messages",
	"channels_post_message":  "post_channel_message",
	"run_migration_preview":  "preview_migration",
}

func TestDeprecatedAliasesAreCallableButNotListed(t *testing.T) {
	e := newEnv(t, &fakeModule{wpmigrate: true})
	a := e.agent("*")
	names := a.toolNames()
	aliasOf := map[string]string{}
	for _, tool := range e.app.Deps.MCP.Tools() {
		for _, old := range tool.Aliases {
			aliasOf[old] = tool.Name
		}
	}
	for old, current := range deprecatedAliases {
		if contains(names, old) {
			t.Errorf("tools/list shows the deprecated name %s", old)
		}
		if !contains(names, current) {
			continue // channel tools need the workagents module (see channels_mcp_test)
		}
		if aliasOf[old] != current {
			t.Errorf("%s does not keep the alias %s", current, old)
		}
	}
	if len(aliasOf) != len(deprecatedAliases)-3 {
		t.Errorf("unexpected aliases registered: %v", aliasOf)
	}
	// The old name still runs the tool, and the audit row names the current tool.
	if got := a.ok("run_migration_preview", map[string]any{"migration_id": "m1"}); str(got, "status") != "previewed" {
		t.Fatalf("alias call: %v", got)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND target_id = 'preview_migration'`); n != 1 {
		t.Fatalf("alias audit rows under the current name: %d", n)
	}
	// Aliases obey scopes like their tool.
	if errObj := e.agent("content.view").fails("run_migration_preview", map[string]any{"migration_id": "m1"}); str(errObj, "code") != "forbidden" {
		t.Fatalf("alias without scope: %v", errObj)
	}
	// And toolset filters.
	if errObj := e.agentAt(a.key, "?toolsets=content", nil).fails("run_migration_preview", map[string]any{"migration_id": "m1"}); str(errObj, "code") != "forbidden" {
		t.Fatalf("alias hidden by toolsets: %v", errObj)
	}
}
