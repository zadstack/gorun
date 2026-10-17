package admin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
)

const connectionMCPVersion = "2025-11-25"

func supportedConnectionMCPVersion(version string) bool {
	switch version {
	case connectionMCPVersion, "2025-06-18", "2025-03-26":
		return true
	default:
		return false
	}
}

type connectionRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func connectionRPCFrame(raw []byte, id int) (json.RawMessage, bool, error) {
	var msg connectionRPC
	if json.Unmarshal(raw, &msg) != nil || msg.JSONRPC != "2.0" || msg.ID != id {
		return nil, false, nil
	}
	if len(msg.Error) > 0 && string(msg.Error) != "null" {
		return nil, true, errors.New("MCP provider returned a protocol error")
	}
	if len(msg.Result) == 0 {
		return nil, true, errors.New("MCP result missing")
	}
	return msg.Result, true, nil
}
func connectionRPCResponse(resp *http.Response, id int) (json.RawMessage, error) {
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxConnectionResponse+1))
		scanner.Buffer(make([]byte, 4096), maxConnectionResponse)
		var data []string
		size := 0
		for scanner.Scan() {
			line := scanner.Text()
			size += len(line) + 1
			if size > maxConnectionResponse {
				return nil, errors.New("MCP response too large")
			}
			if line == "" && len(data) > 0 {
				if result, matched, err := connectionRPCFrame([]byte(strings.Join(data, "\n")), id); matched {
					return result, err
				}
				data = nil
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if scanner.Err() != nil {
			return nil, errors.New("invalid SSE response")
		}
		if len(data) > 0 {
			if result, matched, err := connectionRPCFrame([]byte(strings.Join(data, "\n")), id); matched {
				return result, err
			}
		}
	} else {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxConnectionResponse+1))
		if err != nil || len(raw) > maxConnectionResponse {
			return nil, errors.New("MCP response unreadable or too large")
		}
		if result, matched, err := connectionRPCFrame(raw, id); matched {
			return result, err
		}
	}
	return nil, errors.New("matching MCP response missing")
}
func callConnectionMCP(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets, session, version, method string, id int, params any) (*http.Response, error) {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != 0 {
		body["id"] = id
	}
	if params != nil {
		body["params"] = params
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, errConnectionInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("MCP provider could not be reached safely")
	}
	if err = connectionStatus(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}
func probeMCPConnection(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets) (connectionProbeResult, error) {
	var result connectionProbeResult
	resp, err := callConnectionMCP(ctx, client, c, s, "", "", "initialize", 1, map[string]any{"protocolVersion": connectionMCPVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "GoRun connection check", "version": "1"}})
	if err != nil {
		return result, err
	}
	session := resp.Header.Get("Mcp-Session-Id")
	raw, err := connectionRPCResponse(resp, 1)
	resp.Body.Close()
	if err != nil {
		return result, err
	}
	if len(session) > 256 || strings.ContainsAny(session, "\r\n") {
		return result, errors.New("invalid MCP session")
	}
	var init struct {
		Protocol     string                     `json:"protocolVersion"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &init) != nil || !supportedConnectionMCPVersion(init.Protocol) {
		return result, errors.New("MCP server returned an unsupported Streamable HTTP protocol version")
	}
	if _, ok := init.Capabilities["tools"]; !ok {
		return result, errors.New("MCP server does not advertise tool discovery")
	}
	resp, err = callConnectionMCP(ctx, client, c, s, session, init.Protocol, "notifications/initialized", 0, nil)
	if err != nil {
		return result, err
	}
	resp.Body.Close()
	tools, err := listConnectionMCPTools(ctx, func(ctx context.Context, params map[string]any) (json.RawMessage, error) {
		resp, err := callConnectionMCP(ctx, client, c, s, session, init.Protocol, "tools/list", 2, params)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return connectionRPCResponse(resp, 2)
	})
	if err != nil {
		return result, err
	}
	result.Count = len(tools)
	result.Protocol = init.Protocol
	return result, nil
}

// Discovery is atomic: neither connection checks nor Studio may accept a partial
// catalog, including when a later page fails or exceeds a resource limit.
const (
	maxMCPToolPages        = 16
	maxMCPTools            = 200
	maxMCPToolCatalogBytes = 4 << 20
	maxMCPCursorBytes      = 4096
)

func listConnectionMCPTools(ctx context.Context, request func(context.Context, map[string]any) (json.RawMessage, error)) ([]studioTool, error) {
	tools := make([]studioTool, 0)
	names := make(map[string]bool)
	cursors := make(map[string]bool)
	cursor, totalBytes := "", 0
	for page := 0; page < maxMCPToolPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := request(ctx, params)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		totalBytes += len(raw)
		if len(raw) > maxConnectionResponse || totalBytes > maxMCPToolCatalogBytes {
			return nil, errors.New("MCP tool catalog too large")
		}
		var listed struct {
			Tools []studioTool    `json:"tools"`
			Next  json.RawMessage `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &listed) != nil || listed.Tools == nil {
			return nil, errors.New("invalid MCP tools response")
		}
		if len(tools)+len(listed.Tools) > maxMCPTools {
			return nil, errors.New("MCP tool catalog has too many tools")
		}
		for _, tool := range listed.Tools {
			var schema map[string]json.RawMessage
			var schemaType string
			if !validMCPToolName(tool.Name) || json.Unmarshal(tool.InputSchema, &schema) != nil || json.Unmarshal(schema["type"], &schemaType) != nil || schemaType != "object" {
				return nil, errors.New("invalid MCP tool metadata")
			}
			if names[tool.Name] {
				return nil, errors.New("duplicate MCP tool name")
			}
			names[tool.Name] = true
		}
		tools = append(tools, listed.Tools...)
		cursor = ""
		if len(listed.Next) != 0 && string(listed.Next) != "null" && json.Unmarshal(listed.Next, &cursor) != nil {
			return nil, errors.New("invalid MCP pagination cursor")
		}
		if cursor == "" {
			return tools, nil
		}
		if len(cursor) > maxMCPCursorBytes || cursors[cursor] {
			return nil, errors.New("invalid or repeated MCP pagination cursor")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("MCP tool catalog has too many pages")
}

func validMCPToolName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return false
		}
	}
	return true
}
