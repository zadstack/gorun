package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/config"
	mcpmodule "github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type canvasBearer string

func (b canvasBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	next := r.Clone(r.Context())
	next.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(next)
}
func canvasTool(t *testing.T, s *site, secret, name string, in any) (map[string]any, error) {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "canvas-test", Version: "1"}, nil)
	session, e := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: s.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1, HTTPClient: &http.Client{Transport: canvasBearer(secret)}}, nil)
	if e != nil {
		return nil, e
	}
	defer session.Close()
	result, e := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if e != nil {
		return nil, e
	}
	if result.IsError {
		return nil, errors.New("MCP rejected request")
	}
	if len(result.Content) != 1 {
		return nil, errors.New("unexpected MCP result")
	}
	block, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		return nil, errors.New("unexpected content")
	}
	var out map[string]any
	if e = json.Unmarshal([]byte(block.Text), &out); e != nil {
		return nil, e
	}
	data, _ := out["data"].(map[string]any)
	return data, nil
}
func TestCanvasAgentGrantRunAppendAndRevocation(t *testing.T) {
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), admin.New(), mcpmodule.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	s := &site{t: t, srv: srv, app: app, c: newClient()}
	u := s.login("delegation@example.com", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, key, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Research agent", u.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	otherSecret, _, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Other agent", u.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	path := newCanvas(t, s)
	board := strings.TrimPrefix(path, "/admin/canvases/")
	promptID, agentID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	prompt := map[string]any{"id": promptID, "kind": "prompt", "body": "Prepare a draft", "x": 0, "y": 0}
	agent := map[string]any{"id": agentID, "kind": "agent", "agent_id": key.ID, "x": 300, "y": 0}
	in := canvasInput(prompt, agent)
	in["edges"] = []map[string]string{{"from": promptID, "to": agentID}}
	if code, out := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	previewIn := map[string]any{"version": 2, "node_id": agentID}
	if code, _ := studioJSON(t, s, path+"/operations/preview", previewIn, true); code != 422 {
		t.Fatal("ungranted preview", code)
	}
	grant := map[string]any{"agent_id": key.ID, "read": true, "append": true, "run": true, "node_ids": []string{promptID}}
	if code, out := studioJSON(t, s, path+"/operations/grants", grant, true); code != 200 {
		t.Fatalf("grant %d %v", code, out)
	}
	code, preview := studioJSON(t, s, path+"/operations/preview", previewIn, true)
	if code != 200 {
		t.Fatalf("preview %d %v", code, preview)
	}
	code, out := studioJSON(t, s, path+"/operations/runs", map[string]any{"request_id": "33333333-3333-4333-8333-333333333333", "review_token": preview["review_token"]}, true)
	if code != 202 {
		t.Fatal("run", code, out)
	}
	run := out["run"].(map[string]any)
	if run["status"] != "waiting_agent" {
		t.Fatal(run)
	}
	input := map[string]any{"board_id": board, "run_id": run["id"]}
	if _, e = canvasTool(t, s, otherSecret, "canvases.context", input); e == nil {
		t.Fatal("other key inherited private board")
	}
	contextual, e := canvasTool(t, s, secret, "canvases.context", input)
	if e != nil || contextual["prompt"] != "Prepare a draft" {
		t.Fatal(contextual, e)
	}
	input["text"] = "Agent-authored result"
	input["context_token"] = contextual["context_token"]
	if _, e = canvasTool(t, s, secret, "canvases.append_result", input); e != nil {
		t.Fatal(e)
	}
	if _, e = canvasTool(t, s, secret, "canvases.append_result", input); e != nil {
		t.Fatal("identical retry", e)
	}
	input["text"] = "Changed retry"
	if _, e = canvasTool(t, s, secret, "canvases.append_result", input); e == nil {
		t.Fatal("changed retry accepted")
	}
	doc := canvasGet(t, s, path)
	if len(doc["cards"].([]any)) != 3 {
		t.Fatal("duplicate result")
	}
	grant["read"] = false
	grant["append"] = false
	grant["run"] = false
	if code, _ = studioJSON(t, s, path+"/operations/grants", grant, true); code != 200 {
		t.Fatal(code)
	}
	if _, e = canvasTool(t, s, secret, "canvases.context", map[string]any{"board_id": board, "run_id": run["id"]}); e == nil {
		t.Fatal("revoked grant still read")
	}
}
