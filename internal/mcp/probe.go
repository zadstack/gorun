package mcp

import (
	"context"
	"fmt"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/admin"
)

// Probe implements admin.MCPProbe: it serves one MCP session over in-memory
// transports, acting as principal exactly like /mcp would (same tools,
// scope filter and instructions), runs initialize and tools/list as a
// client, and reports what the key sees. No tool is called.
func (s *server) Probe(ctx, principal context.Context) (admin.MCPProbeResult, error) {
	var out admin.MCPProbeResult
	srv := s.newSDKServer(func(context.Context) (context.Context, error) { return principal, nil }, nil, toolView{})
	clientT, serverT := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		return out, fmt.Errorf("mcp: probe: serve: %w", err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "gorun-admin-test", Version: serverVersion}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return out, fmt.Errorf("mcp: probe: initialize: %w", err)
	}
	defer cs.Close()
	if init := cs.InitializeResult(); init != nil {
		out.ProtocolVersion = init.ProtocolVersion
		if init.ServerInfo != nil {
			out.ServerName, out.ServerVersion = init.ServerInfo.Name, init.ServerInfo.Version
		}
	}
	sets := map[string]bool{}
	for tl, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return out, fmt.Errorf("mcp: probe: tools/list: %w", err)
		}
		out.Tools = append(out.Tools, tl.Name)
		if t, ok := s.tool(tl.Name); ok {
			sets[t.ToolsetName()] = true
			if len(t.Scopes) > 0 {
				out.Scoped++
			}
		}
	}
	slices.Sort(out.Tools)
	for name := range sets {
		out.Toolsets = append(out.Toolsets, name)
	}
	slices.Sort(out.Toolsets)
	return out, nil
}
