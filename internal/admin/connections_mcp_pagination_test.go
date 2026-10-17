package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func catalogTool(name string) map[string]any {
	return map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}}
}

// Exercise actual HTTP negotiation, session propagation, JSON and SSE, discovery
// from both callers, and invocation of a tool that exists only on page two.
func TestMCPPaginatedHTTPDiscovery(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			initialized := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Error("request decode")
					w.WriteHeader(400)
					return
				}
				var result any
				if req.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != "paged-session" || r.Header.Get("MCP-Protocol-Version") != connectionMCPVersion) {
					t.Error("session/protocol missing")
				}
				switch req.Method {
				case "initialize":
					initialized = false
					w.Header().Set("Mcp-Session-Id", "paged-session")
					result = map[string]any{"protocolVersion": connectionMCPVersion, "capabilities": map[string]any{"tools": map[string]any{}}}
				case "notifications/initialized":
					initialized = true
					w.WriteHeader(202)
					return
				case "tools/list":
					if !initialized {
						t.Error("discovery before initialization")
					}
					if req.Params["cursor"] == nil {
						result = map[string]any{"tools": []any{catalogTool("first")}, "nextCursor": "opaque+/= cursor"}
					} else if req.Params["cursor"] == "opaque+/= cursor" {
						result = map[string]any{"tools": []any{catalogTool("second")}}
					} else {
						t.Error("cursor changed")
					}
				case "tools/call":
					if req.Params["name"] != "second" {
						t.Error("wrong tool")
					}
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "second-page success"}}}
				default:
					t.Error("unexpected method")
				}
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.Write(raw)
				}
			}))
			defer srv.Close()
			c := connectionRecord{Endpoint: srv.URL}
			probe, err := probeMCPConnection(context.Background(), srv.Client(), c, connectionSecrets{})
			if err != nil || probe.Count != 2 {
				t.Fatalf("probe: %+v %v", probe, err)
			}
			session, err := openStudioMCP(context.Background(), srv.Client(), c, connectionSecrets{})
			if err != nil {
				t.Fatal(err)
			}
			tools, err := session.tools(context.Background())
			if err != nil || len(tools) != 2 || tools[1].Name != "second" {
				t.Fatalf("Studio: %+v %v", tools, err)
			}
			result, err := session.call(context.Background(), tools[1].Name, map[string]any{})
			if err != nil || !strings.Contains(string(result), "second-page success") {
				t.Fatalf("call: %s %v", result, err)
			}
		})
	}
}

func TestMCPRejectsPartialCatalog(t *testing.T) {
	cases := []struct {
		name   string
		second string
	}{
		{"cycle", `{"tools":[],"nextCursor":"next"}`},
		{"duplicate", `{"tools":[{"name":"first","inputSchema":{"type":"object"}}]}`},
		{"malformed", `{"tools":`},
		{"missing tools", `{}`},
		{"null tools", `{"tools":null}`},
		{"invalid schema", `{"tools":[{"name":"second","inputSchema":{"type":"string"}}]}`},
		{"invalid name", `{"tools":[{"name":"bad name","inputSchema":{"type":"object"}}]}`},
		{"number cursor", `{"tools":[],"nextCursor":2}`},
		{"long cursor", `{"tools":[],"nextCursor":"` + strings.Repeat("x", maxMCPCursorBytes+1) + `"}`},
		{"page bytes", strings.Repeat(" ", maxConnectionResponse+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			tools, err := listConnectionMCPTools(context.Background(), func(context.Context, map[string]any) (json.RawMessage, error) {
				calls++
				if calls == 1 {
					return json.RawMessage(`{"tools":[{"name":"first","inputSchema":{"type":"object"}}],"nextCursor":"next"}`), nil
				}
				return json.RawMessage(tc.second), nil
			})
			if err == nil || tools != nil || calls != 2 {
				t.Fatalf("partial catalog accepted: %v %v calls=%d", tools, err, calls)
			}
		})
	}
}

func TestMCPPaginationLimitsAndCancellation(t *testing.T) {
	for _, kind := range []string{"pages", "tools", "bytes", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			tools, err := listConnectionMCPTools(ctx, func(context.Context, map[string]any) (json.RawMessage, error) {
				calls++
				entries := []any{}
				if kind == "tools" {
					for n := 0; n <= maxMCPTools; n++ {
						entries = append(entries, catalogTool(fmt.Sprintf("tool%d", n)))
					}
				}
				if kind == "bytes" {
					tool := catalogTool(fmt.Sprintf("tool%d", calls))
					tool["description"] = strings.Repeat("x", maxConnectionResponse-1024)
					entries = append(entries, tool)
				}
				if kind == "cancel" {
					cancel()
				}
				raw, _ := json.Marshal(map[string]any{"tools": entries, "nextCursor": fmt.Sprint(calls)})
				return raw, nil
			})
			if err == nil || tools != nil {
				t.Fatalf("limit accepted %v", err)
			}
			if kind == "pages" && calls != maxMCPToolPages {
				t.Fatalf("pages=%d", calls)
			}
			if kind == "bytes" && calls != 5 {
				t.Fatalf("bytes calls=%d", calls)
			}
			if kind == "cancel" && calls != 1 {
				t.Fatalf("cancel calls=%d", calls)
			}
		})
	}
}

func TestMCPHTTPSecondPageFailureDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int            `json:"id"`
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Params["cursor"] != nil {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"secret-token-private"}}`)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"first","inputSchema":{"type":"object"}}],"nextCursor":"next"}}`)
	}))
	defer srv.Close()
	m := studioSession{client: srv.Client(), connection: connectionRecord{Endpoint: srv.URL}}
	tools, err := m.tools(context.Background())
	if err == nil || tools != nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe partial result: %v %v", tools, err)
	}
}

func TestMCPNullCursorAndQualifiedNames(t *testing.T) {
	tools, err := listConnectionMCPTools(context.Background(), func(context.Context, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"tools":[{"name":"provider/tool:read","inputSchema":{"type":"object"}}],"nextCursor":null}`), nil
	})
	if err != nil || len(tools) != 1 {
		t.Fatalf("compatible terminal catalog rejected: %v %v", tools, err)
	}
}
