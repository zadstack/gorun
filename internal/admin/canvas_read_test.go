package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	mcpmodule "github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestCanvasReadMCPGrantFilterAndDenial(t *testing.T) {
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), admin.New(), mcpmodule.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	s := &site{t: t, srv: srv, app: app, c: newClient()}
	u := s.login("canvas-read@example.com", "admin", "en")

	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	agentSecret, agentKey, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Reader agent", u.ID, []string{"canvas.read", "content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	ownerSecret, _, err := keys.CreateKind(context.Background(), auth.KeyKindAPI, "Owner REST", u.ID, []string{"canvas.read", "content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	noScopeSecret, _, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "No canvas scope", u.ID, []string{"content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}

	path := newCanvas(t, s)
	boardID := strings.TrimPrefix(path, "/admin/canvases/")
	noteID, agentNodeID, hiddenID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	note := map[string]any{"id": noteID, "kind": "note", "title": "Shared", "body": "Visible note", "x": 0, "y": 0}
	agentCard := map[string]any{"id": agentNodeID, "kind": "agent", "agent_id": agentKey.ID, "x": 300, "y": 0}
	hidden := map[string]any{"id": hiddenID, "kind": "note", "title": "Private", "body": "Hidden note", "x": 100, "y": 100}
	in := canvasInput(note, agentCard, hidden)
	in["edges"] = []map[string]string{{"from": noteID, "to": agentNodeID}}
	if code, out := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}

	if env := canvasToolEnv(t, s, noScopeSecret, "list_canvases", map[string]any{}); env["ok"] != false {
		t.Fatalf("missing canvas.read must fail: %v", env)
	}

	denied := canvasToolEnv(t, s, agentSecret, "list_canvases", map[string]any{})
	if denied["ok"] != true {
		t.Fatalf("list without grant: %v", denied)
	}
	data := denied["data"].(map[string]any)
	if data["total"] != float64(0) || len(data["items"].([]any)) != 0 {
		t.Fatalf("ungranted agent listed boards: %v", data)
	}
	if code := strMap(envError(canvasToolEnv(t, s, agentSecret, "get_canvas", map[string]any{"id": boardID})), "code"); code != "forbidden" {
		t.Fatalf("want forbidden without grant, got %q", code)
	}

	grant := map[string]any{"agent_id": agentKey.ID, "read": true, "append": false, "run": false, "node_ids": []string{noteID}}
	if code, out := studioJSON(t, s, path+"/operations/grants", grant, true); code != 200 {
		t.Fatalf("grant %d %v", code, out)
	}

	listed := canvasToolEnv(t, s, agentSecret, "list_canvases", map[string]any{})
	if listed["ok"] != true {
		t.Fatalf("list with grant: %v", listed)
	}
	items := listed["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != boardID {
		t.Fatalf("granted list: %v", items)
	}

	got := canvasToolEnv(t, s, agentSecret, "get_canvas", map[string]any{"id": boardID})
	if got["ok"] != true {
		t.Fatalf("get with grant: %v", got)
	}
	doc := got["data"].(map[string]any)
	cards := doc["cards"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["id"] != noteID {
		t.Fatalf("grant filter cards: %v", cards)
	}
	if edges := doc["edges"].([]any); len(edges) != 0 {
		t.Fatalf("edges to ungranted nodes must be omitted: %v", edges)
	}

	ownerList := canvasToolEnv(t, s, ownerSecret, "list_canvases", map[string]any{})
	if ownerList["ok"] != true {
		t.Fatalf("owner list: %v", ownerList)
	}
	if ownerList["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("owner should see board without grant: %v", ownerList)
	}
	ownerGet := canvasToolEnv(t, s, ownerSecret, "get_canvas", map[string]any{"id": boardID})
	if ownerGet["ok"] != true {
		t.Fatalf("owner get: %v", ownerGet)
	}
	if len(ownerGet["data"].(map[string]any)["cards"].([]any)) != 3 {
		t.Fatalf("owner gets full board: %v", ownerGet["data"])
	}

	res, err := canvasResource(t, s, agentSecret, "gorun://canvases/"+boardID)
	if err != nil {
		t.Fatal(err)
	}
	var resourceDoc map[string]any
	if err := json.Unmarshal([]byte(res), &resourceDoc); err != nil {
		t.Fatal(err)
	}
	if len(resourceDoc["cards"].([]any)) != 1 {
		t.Fatalf("resource must apply grant filter: %v", resourceDoc)
	}
}

func TestCanvasReadRESTMirror(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	s := &site{t: t, srv: httptest.NewServer(app.Handler), app: app, c: newClient()}
	t.Cleanup(s.srv.Close)
	u := s.login("canvas-rest@example.com", "admin", "en")
	path := newCanvas(t, s)
	boardID := strings.TrimPrefix(path, "/admin/canvases/")
	note := map[string]any{"id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "kind": "note", "title": "REST", "body": "note", "x": 0, "y": 0}
	if code, out := studioJSON(t, s, path+"/document", canvasInput(note), true); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}

	okClient := apitest.New(t, app, apitest.Options{User: u, Scopes: []string{"canvas.read", "content.view"}})
	res := okClient.Do("GET", "/v1/canvases", nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list %d %s", res.Code, res.Body.String())
	}
	var list struct {
		OK   bool                  `json:"ok"`
		Data []admin.CanvasSummary `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || !list.OK || len(list.Data) != 1 || list.Data[0].ID != boardID {
		t.Fatalf("list body: %s err=%v", res.Body.String(), err)
	}

	res = okClient.Do("GET", "/v1/canvases/"+boardID, nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get %d %s", res.Code, res.Body.String())
	}

	denied := apitest.New(t, app, apitest.Options{User: u, Scopes: []string{"content.view"}})
	if res = denied.Do("GET", "/v1/canvases", nil, nil); res.Code != http.StatusForbidden {
		t.Fatalf("missing canvas.read: %d %s", res.Code, res.Body.String())
	}

	agent := apitest.New(t, app, apitest.Options{User: u, Kind: auth.KeyKindAgent, Scopes: []string{"canvas.read", "content.view"}})
	if res = agent.Do("GET", "/v1/canvases", nil, nil); res.Code != http.StatusForbidden {
		t.Fatalf("agent key on REST: %d %s", res.Code, res.Body.String())
	}
}

func canvasToolEnv(t *testing.T, s *site, secret, name string, in any) map[string]any {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "canvas-read-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: s.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: canvasBearer(secret)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		t.Fatal(err)
	}
	block, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content type %T", result.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(block.Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func canvasResource(t *testing.T, s *site, secret, uri string) (string, error) {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "canvas-read-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: s.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: canvasBearer(secret)},
	}, nil)
	if err != nil {
		return "", err
	}
	defer session.Close()
	res, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	if len(res.Contents) == 0 {
		return "", errors.New("empty resource")
	}
	return res.Contents[0].Text, nil
}

func envError(env map[string]any) map[string]any {
	e, _ := env["error"].(map[string]any)
	return e
}

func strMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
