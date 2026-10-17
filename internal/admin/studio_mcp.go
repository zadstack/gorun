package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type studioTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type studioSession struct {
	client     *http.Client
	connection connectionRecord
	secrets    connectionSecrets
	session    string
	protocol   string
}

func openStudioMCP(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets) (*studioSession, error) {
	m := &studioSession{client: client, connection: c, secrets: s}
	resp, e := callConnectionMCP(ctx, client, c, s, "", "", "initialize", 1, map[string]any{"protocolVersion": connectionMCPVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "GoRun Studio", "version": "1"}})
	if e != nil {
		return nil, e
	}
	m.session = resp.Header.Get("Mcp-Session-Id")
	raw, e := connectionRPCResponse(resp, 1)
	resp.Body.Close()
	if e != nil || len(m.session) > 256 || strings.ContainsAny(m.session, "\r\n") {
		return nil, errStudio
	}
	var result struct {
		Protocol     string                     `json:"protocolVersion"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &result) != nil || !supportedConnectionMCPVersion(result.Protocol) || result.Capabilities["tools"] == nil {
		return nil, errStudio
	}
	m.protocol = result.Protocol
	resp, e = callConnectionMCP(ctx, client, c, s, m.session, m.protocol, "notifications/initialized", 0, nil)
	if e != nil {
		return nil, e
	}
	resp.Body.Close()
	return m, nil
}
func (m *studioSession) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	resp, e := callConnectionMCP(ctx, m.client, m.connection, m.secrets, m.session, m.protocol, method, 2, params)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	return connectionRPCResponse(resp, 2)
}
func (m *studioSession) tools(ctx context.Context) ([]studioTool, error) {
	return listConnectionMCPTools(ctx, func(ctx context.Context, params map[string]any) (json.RawMessage, error) {
		return m.request(ctx, "tools/list", params)
	})
}

func (m *studioSession) call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	return m.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
}
