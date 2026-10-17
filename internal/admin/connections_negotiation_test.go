package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestMCPConnectionsAndStudioNegotiateSupportedVersions(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05", "2099-01-01", ""} {
		for _, mode := range []string{"probe", "studio"} {
			t.Run(mode+"/"+version, func(t *testing.T) {
				supported := slices.Contains([]string{"2025-11-25", "2025-06-18", "2025-03-26"}, version)
				var methods []string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var in struct {
						Method string `json:"method"`
						Params struct {
							Protocol string `json:"protocolVersion"`
						} `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					methods = append(methods, in.Method)
					w.Header().Set("Content-Type", "application/json")
					if in.Method == "initialize" {
						if in.Params.Protocol != "2025-11-25" {
							t.Error("client did not offer latest supported version")
						}
						w.Header().Set("Mcp-Session-Id", "negotiated-session")
						fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":%q,"capabilities":{"tools":{}}}}`, version)
						return
					}
					if !supported {
						t.Error("continued unsupported session")
					}
					if r.Header.Get("MCP-Protocol-Version") != version || r.Header.Get("Mcp-Session-Id") != "negotiated-session" {
						t.Error("negotiated protocol/session not retained")
					}
					switch in.Method {
					case "notifications/initialized":
						w.WriteHeader(202)
					case "tools/list":
						fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read_status","inputSchema":{"type":"object"}}]}}`)
					case "tools/call":
						fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"ready"}]}}`)
					default:
						t.Error("unexpected operation")
						w.WriteHeader(400)
					}
				}))
				defer srv.Close()
				c := connectionRecord{Endpoint: srv.URL}
				if mode == "probe" {
					result, err := probeMCPConnection(context.Background(), srv.Client(), c, connectionSecrets{})
					if !supported {
						if err == nil {
							t.Fatal("accepted unsupported protocol")
						}
					} else if err != nil || result.Protocol != version || result.Count != 1 {
						t.Fatalf("probe result=%+v err=%v", result, err)
					}
				} else {
					session, err := openStudioMCP(context.Background(), srv.Client(), c, connectionSecrets{})
					if !supported {
						if err == nil {
							t.Fatal("accepted unsupported protocol")
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if _, err = session.tools(context.Background()); err != nil {
							t.Fatal(err)
						}
						if _, err = session.call(context.Background(), "read_status", map[string]any{}); err != nil {
							t.Fatal(err)
						}
					}
				}
				want := 1
				if supported {
					want = 3
					if mode == "studio" {
						want = 4
					}
				}
				if len(methods) != want {
					t.Fatalf("unexpected requests %v", methods)
				}
			})
		}
	}
}
