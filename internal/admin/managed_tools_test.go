package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/zadstack/gorun/internal/httpx"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

type managedFixture struct {
	h                     *handlers
	ctx                   context.Context
	board                 *canvasBoard
	prepared              canvasPrepared
	client                *http.Client
	alias                 string
	modelCalls, toolCalls int
	drift                 bool
	repeat                bool
	args                  string
	proposedAlias         string
	beforeTool            func()
	reply                 string
	cookie                string
	csrf                  string
}

func managedOperationFixture(t *testing.T) (*handlers, context.Context, *canvasBoard, canvasPrepared) {
	t.Helper()
	modules := []kernel.Module{settingsmod.New(), auth.New(), content.New()}
	modules = append(modules, New())
	app, e := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, modules, kernel.BootOptions{StartJobs: false})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { app.Close() })
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	u, e := users.Create(context.Background(), "fixture@example.com", "Owner", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithUser(context.Background(), u)
	h := &handlers{d: app.Deps, users: users, keys: keys, aiLimit: newAILimiter()}
	_, key, e := keys.CreateKind(ctx, auth.KeyKindAgent, "Worker", u.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view", "content.edit"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	id, e := h.canvasCreate(ctx, "Board")
	if e != nil {
		t.Fatal(e)
	}
	cards := []canvasCard{{ID: "11111111-1111-4111-8111-111111111111", Kind: "prompt", Body: "Summarize"}, {ID: "22222222-2222-4222-8222-222222222222", Kind: "agent", AgentID: key.ID}, {ID: "33333333-3333-4333-8333-333333333333", Kind: "schedule", Interval: "hourly"}}
	edges := []canvasEdge{{cards[0].ID, cards[1].ID}, {cards[2].ID, cards[1].ID}}
	if _, e = app.Deps.DB.Exec(`UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?`, canvasEncode(cards), canvasEncode(edges), id); e != nil {
		t.Fatal(e)
	}
	if _, e = app.Deps.DB.Exec(`INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,1,1,1,?)`, id, key.ID, canvasEncode([]string{cards[0].ID})); e != nil {
		t.Fatal(e)
	}
	b, e := h.canvasOwned(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	p, e := h.canvasPrepare(ctx, b, cards[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	return h, ctx, b, p
}

func newManagedFixture(t *testing.T) *managedFixture {
	t.Helper()
	h, ctx, b, p := managedOperationFixture(t)
	c := connectionRecord{ID: db.NewID(), Version: db.NewID(), Kind: "ai", Name: "Named AI", Endpoint: "https://api.openai.com/v1", Provider: "openai", Model: "default"}
	if e := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "reviewed-model"})
	if e != nil {
		t.Fatal(e)
	}
	secret, e := h.encryptConnectionSecrets(connectionSecrets{Token: "private-mcp-token"})
	if e != nil {
		t.Fatal(e)
	}
	mcp := connectionRecord{ID: db.NewID(), Version: db.NewID(), Kind: "mcp", Name: "Archive", Endpoint: "https://archive.example/mcp", Secret: secret}
	if e = h.d.Settings.Set(ctx, connectionPrefix+mcp.ID, mcp); e != nil {
		t.Fatal(e)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":80}},"required":["query"],"additionalProperties":false}`)
	rows, hash, e := canonicalToolCatalog([]studioTool{{Name: "lookup", Description: "Look up an archive entry", InputSchema: schema}})
	if e != nil {
		t.Fatal(e)
	}
	tool := agentManagedTool{ConnectionID: mcp.ID, ConnectionVersion: mcp.Version, Name: rows[0].Name, Description: rows[0].Description, Schema: rows[0].InputSchema, CatalogHash: hash}
	tool.Alias = managedToolAlias(tool)
	if _, e = h.saveAgentBundle(ctx, p.KeyID, "", agentBundle{MCP: []agentMCPReference{{ID: mcp.ID, Version: mcp.Version}}, Tools: &agentToolPolicy{ModelVersion: cfg.Version, Enabled: true, Allow: []agentManagedTool{tool}}}); e != nil {
		t.Fatal(e)
	}
	p, e = h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	f := &managedFixture{h: h, ctx: ctx, board: b, prepared: p, alias: tool.Alias, reply: `{"content":[{"type":"text","text":"Actual archive evidence"}]}`, cookie: "managed-session", csrf: "managed-csrf"}
	sum := sha256.Sum256([]byte(f.cookie))
	_, e = h.d.DB.Exec(`INSERT INTO sessions(id,user_id,csrf_token,ip,ua,expires_at,created_at,reauth_at) VALUES(?,?,?,'','',?,?,?)`, hex.EncodeToString(sum[:]), p.OwnerID, f.csrf, db.FormatTime(time.Now().Add(time.Hour)), db.Now(), db.Now())
	if e != nil {
		t.Fatal(e)
	}
	f.client = &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil {
			t.Fatal("invalid protocol request")
		}
		response := func(code int, v string) *http.Response {
			return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}, "Mcp-Session-Id": []string{"archive-session"}}, Body: io.NopCloser(strings.NewReader(v))}
		}
		if r.URL.Host == "api.openai.com" {
			f.modelCalls++
			if body["model"] != "reviewed-model" || body["parallel_tool_calls"] != false {
				t.Error("unreviewed model config")
			}
			if f.modelCalls == 1 || f.repeat {
				alias := f.alias
				if f.proposedAlias != "" {
					alias = f.proposedAlias
				}
				args := `{"query":"evidence"}`
				if f.args != "" {
					args = f.args
				}
				functionJSON, _ := json.Marshal(managedFunction{Name: alias, Arguments: args})
				return response(200, fmt.Sprintf(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_%d","type":"function","function":%s}]}}]}`, f.modelCalls, functionJSON)), nil
			}
			if !strings.Contains(string(raw), `Actual archive evidence`) || !strings.Contains(string(raw), `"role":"tool"`) {
				t.Error("actual tool result not in continuation")
			}
			return response(200, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Private evidence-based draft"}}]}`), nil
		}
		if r.URL.Host != "archive.example" || r.Header.Get("Authorization") != "Bearer private-mcp-token" {
			t.Fatal("wrong endpoint/credentials")
		}
		switch body["method"] {
		case "initialize":
			return response(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}`), nil
		case "notifications/initialized":
			return response(202, ``), nil
		case "tools/list":
			desc := "Look up an archive entry"
			if f.drift {
				desc = "Changed catalog"
			}
			catalog, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{"tools": []studioTool{{Name: "lookup", Description: desc, InputSchema: schema}}}})
			return response(200, string(catalog)), nil
		case "tools/call":
			f.toolCalls++
			if r.Header.Get("Mcp-Session-Id") != "archive-session" {
				t.Error("missing MCP session")
			}
			params := body["params"].(map[string]any)
			if params["name"] != "lookup" || params["arguments"].(map[string]any)["query"] != "evidence" {
				t.Error("changed approved arguments")
			}
			if f.beforeTool != nil {
				f.beforeTool()
			}
			return response(200, `{"jsonrpc":"2.0","id":2,"result":`+f.reply+`}`), nil
		}
		t.Fatalf("unexpected protocol %s", raw)
		return nil, nil
	})}
	return f
}
func (f *managedFixture) run(t *testing.T) string {
	t.Helper()
	r, e := f.h.canvasEnqueue(f.ctx, f.prepared, db.NewID())
	if e != nil {
		t.Fatal(e)
	}
	f.work(t, r.ID)
	return r.ID
}
func (f *managedFixture) work(t *testing.T, id string) {
	t.Helper()
	if e := f.h.managedToolsUsing(f.ctx, json.RawMessage(`{"id":"`+id+`"}`), f.client, f.client); e != nil {
		t.Fatal(e)
	}
}
func (f *managedFixture) status(t *testing.T, id string) string {
	t.Helper()
	var s string
	if e := f.h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, id).Scan(&s); e != nil {
		t.Fatal(e)
	}
	return s
}
func (f *managedFixture) approval(t *testing.T, id, method string, body any, csrf bool) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/admin/canvases/"+f.board.ID+"/operations/runs/"+id+"/tool", strings.NewReader(string(raw)))
	r.AddCookie(&http.Cookie{Name: "gp_session", Value: f.cookie})
	r.Header.Set("Content-Type", "application/json")
	if csrf {
		r.Header.Set("X-CSRF-Token", f.csrf)
	}
	router := chi.NewRouter()
	router.Use(auth.SessionMiddleware(f.h.d), auth.CSRFMiddleware())
	router.MethodFunc(method, "/admin/canvases/{id}/operations/runs/{run}/tool", f.h.managedToolApproval)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func (f *managedFixture) approve(t *testing.T, id string) map[string]any {
	t.Helper()
	code, review := f.approval(t, id, "GET", nil, false)
	if code != 200 {
		t.Fatal(code, review)
	}
	in := map[string]any{"approval_id": review["approval_id"], "revision": review["revision"], "approve": true}
	code, out := f.approval(t, id, "POST", in, true)
	if code != 200 {
		t.Fatal(code, out)
	}
	return in
}
func TestManagedToolsActualProtocolApprovalAndPrivateResult(t *testing.T) {
	f := newManagedFixture(t)
	id := f.run(t)
	if f.modelCalls != 1 || f.toolCalls != 0 || f.status(t, id) != "waiting_tool" {
		t.Fatal("tool executed before approval", f.modelCalls, f.toolCalls, f.status(t, id))
	}
	in := f.approve(t, id)
	if f.toolCalls != 0 {
		t.Fatal("approval performed network action")
	}
	if code, _ := f.approval(t, id, "POST", in, true); code != 409 {
		t.Fatal("replayed approval", code)
	}
	f.work(t, id)
	if f.status(t, id) != "succeeded" || f.toolCalls != 1 || f.modelCalls != 2 {
		t.Fatal(f.status(t, id), f.toolCalls, f.modelCalls)
	}
	f.work(t, id)
	if f.toolCalls != 1 {
		t.Fatal("replayed completed tool")
	}
	var n int
	f.h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, id).Scan(&n)
	if n != 1 {
		t.Fatal("private note missing")
	}
}
func TestManagedToolsAuthorityRestoreRetiresApproval(t *testing.T) {
	for _, change := range []string{"grant", "role", "permission", "key", "workspace", "connection", "bundle"} {
		t.Run(change, func(t *testing.T) {
			f := newManagedFixture(t)
			id := f.run(t)
			f.approve(t, id)
			exec := func(q string, args ...any) {
				t.Helper()
				if _, e := f.h.d.DB.Exec(q, args...); e != nil {
					t.Fatal(e)
				}
			}
			switch change {
			case "grant":
				exec(`UPDATE admin_canvas_grants SET can_run=0 WHERE board_id=?`, f.board.ID)
				exec(`UPDATE admin_canvas_grants SET can_run=1 WHERE board_id=?`, f.board.ID)
			case "role":
				exec(`UPDATE users SET role='customer' WHERE id=?`, f.prepared.OwnerID)
				exec(`UPDATE users SET role='admin' WHERE id=?`, f.prepared.OwnerID)
			case "permission":
				exec(`DELETE FROM role_permissions WHERE role='admin' AND permission='*'`)
				exec(`INSERT INTO role_permissions(role,permission) VALUES('admin','*')`)
			case "key":
				exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, db.Now(), f.prepared.KeyID)
				exec(`UPDATE api_keys SET revoked_at='' WHERE id=?`, f.prepared.KeyID)
			case "workspace":
				if e := f.h.d.Settings.Set(f.ctx, "site.workspace", "simple"); e != nil {
					t.Fatal(e)
				}
				if e := f.h.d.Settings.Set(f.ctx, "site.workspace", "advanced"); e != nil {
					t.Fatal(e)
				}
			case "connection":
				exec(`UPDATE settings SET value_json=value_json WHERE key LIKE 'connections.profile.%'`)
			case "bundle":
				exec(`UPDATE settings SET value_json=value_json WHERE key=?`, agentBundlePrefix+f.prepared.KeyID)
			}
			f.work(t, id)
			if f.toolCalls != 0 || f.modelCalls != 1 || f.status(t, id) != "failed" {
				t.Fatal("historical authority accepted", f.status(t, id), f.toolCalls, f.modelCalls)
			}
			current, e := f.h.canvasPrepare(f.ctx, f.board, f.prepared.NodeID)
			if e != nil || current.ReviewHash == f.prepared.ReviewHash {
				t.Fatal("fresh review not renewed", e)
			}
			f.prepared = current
			f.modelCalls = 0
			fresh := f.run(t)
			if f.status(t, fresh) != "waiting_tool" {
				t.Fatal("fresh review unavailable")
			}
		})
	}
}
func TestManagedToolsCatalogChangeAndInFlightRevocation(t *testing.T) {
	for _, mode := range []string{"catalog", "during", "nontext", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagedFixture(t)
			id := f.run(t)
			f.approve(t, id)
			switch mode {
			case "catalog":
				f.drift = true
			case "during":
				f.beforeTool = func() {
					_, e := f.h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_run=0 WHERE board_id=?`, f.board.ID)
					if e != nil {
						t.Fatal(e)
					}
					_, e = f.h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_run=1 WHERE board_id=?`, f.board.ID)
					if e != nil {
						t.Fatal(e)
					}
				}
			case "nontext":
				f.reply = `{"content":[{"type":"resource_link","uri":"https://unreviewed.example"}]}`
			case "ambiguous":
				_, e := f.h.d.DB.Exec(`UPDATE admin_canvas_runs SET status='running' WHERE id=?`, id)
				if e != nil {
					t.Fatal(e)
				}
			}
			f.work(t, id)
			if f.modelCalls != 1 {
				t.Fatal("continued stale tool result")
			}
			if mode == "catalog" || mode == "ambiguous" {
				if f.toolCalls != 0 {
					t.Fatal("executed forbidden call")
				}
			} else if f.toolCalls != 1 {
				t.Fatal("actual tool missing")
			}
			if f.status(t, id) != "failed" && f.status(t, id) != "uncertain" {
				t.Fatal("run not terminal")
			}
			f.work(t, id)
			if f.toolCalls > 1 {
				t.Fatal("retried external action")
			}
		})
	}
}
func TestManagedToolsApprovalSessionCSRFRejectAndNoGETMutation(t *testing.T) {
	f := newManagedFixture(t)
	id := f.run(t)
	var before string
	f.h.d.DB.QueryRow(`SELECT document FROM admin_canvas_tool_states WHERE run_id=?`, id).Scan(&before)
	code, review := f.approval(t, id, "GET", nil, false)
	if code != 200 || review["tool"] != "lookup" || review["host"] != "archive.example" {
		t.Fatal(code, review)
	}
	var after string
	f.h.d.DB.QueryRow(`SELECT document FROM admin_canvas_tool_states WHERE run_id=?`, id).Scan(&after)
	if before != after || f.toolCalls != 0 {
		t.Fatal("GET performed action")
	}
	in := map[string]any{"approval_id": review["approval_id"], "revision": review["revision"], "approve": true}
	if code, _ = f.approval(t, id, "POST", in, false); code != 403 {
		t.Fatal("CSRF bypass", code)
	}
	_, e := f.h.d.DB.Exec(`UPDATE sessions SET reauth_at=?`, db.FormatTime(time.Now().Add(-time.Hour)))
	if e != nil {
		t.Fatal(e)
	}
	if code, out := f.approval(t, id, "POST", in, true); code != 403 || out["error"] != "reauth_required" {
		t.Fatal("recent auth bypass", code, out)
	}
	_, e = f.h.d.DB.Exec(`UPDATE sessions SET reauth_at=?`, db.Now())
	if e != nil {
		t.Fatal(e)
	}
	in["approve"] = false
	if code, _ = f.approval(t, id, "POST", in, true); code != 200 {
		t.Fatal(code)
	}
	f.work(t, id)
	if f.toolCalls != 0 || f.status(t, id) != "cancelled" {
		t.Fatal("rejected call ran")
	}
}

func TestManagedToolsBoundsSchemaAndExactAlias(t *testing.T) {
	for _, which := range []string{"unknown", "arguments", "secret", "oversized", "expired", "time", "call_budget"} {
		t.Run(which, func(t *testing.T) {
			f := newManagedFixture(t)
			switch which {
			case "unknown":
				f.proposedAlias = "lookup"
			case "arguments":
				f.args = `{"query":1}`
			case "secret":
				f.args = `{"query":"private-mcp-token"}`
			case "oversized":
				f.args = `{"query":"` + strings.Repeat("a", 5000) + `"}`
			}
			id := f.run(t)
			if which == "unknown" || which == "arguments" || which == "secret" || which == "oversized" {
				if f.status(t, id) != "failed" || f.toolCalls != 0 {
					t.Fatal("unreviewed proposal accepted")
				}
				return
			}
			var raw string
			f.h.d.DB.QueryRow(`SELECT document FROM admin_canvas_tool_states WHERE run_id=?`, id).Scan(&raw)
			var state managedState
			json.Unmarshal([]byte(raw), &state)
			switch which {
			case "expired":
				state.Pending.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
			case "time":
				state.ActiveMillis = 90000
			case "call_budget":
				state.Calls = 4
			}
			if _, e := f.h.d.DB.Exec(`UPDATE admin_canvas_tool_states SET document=? WHERE run_id=?`, canvasEncode(state), id); e != nil {
				t.Fatal(e)
			}
			if code, _ := f.approval(t, id, "GET", nil, false); code != 409 {
				t.Fatal("expired/overbudget proposal visible", code)
			}
			if f.toolCalls != 0 {
				t.Fatal("unapproved call ran")
			}
		})
	}
	for _, schema := range []string{`{"type":"object","$ref":"https://attacker.example/schema"}`, `{"type":"object","properties":{"a":{"$id":"https://attacker.example"}}}`, `{"type":"string"}`, strings.Repeat("{", 9000)} {
		if _, e := resolveManagedSchema(json.RawMessage(schema)); e == nil {
			t.Fatal("unsafe schema accepted")
		}
	}
	a := agentManagedTool{ConnectionID: db.NewID(), ConnectionVersion: db.NewID(), Name: "lookup", CatalogHash: strings.Repeat("a", 64)}
	b := a
	b.ConnectionID = db.NewID()
	if managedToolAlias(a) == managedToolAlias(b) {
		t.Fatal("cross-server alias collision")
	}
}
func TestManagedToolsThreeCallsAreDurablyBounded(t *testing.T) {
	f := newManagedFixture(t)
	f.repeat = true
	id := f.run(t)
	for n := 0; n < 3; n++ {
		if f.status(t, id) != "waiting_tool" || f.toolCalls != n {
			t.Fatal("wrong pause", n, f.status(t, id), f.toolCalls)
		}
		f.approve(t, id)
		f.work(t, id)
	}
	if f.toolCalls != 3 || f.modelCalls != 4 || f.status(t, id) != "failed" {
		t.Fatal("exceeded call bound", f.toolCalls, f.modelCalls, f.status(t, id))
	}
	f.work(t, id)
	if f.toolCalls != 3 {
		t.Fatal("replayed bounded run")
	}
}
func TestManagedToolsSourceHistoryAndModuleCompatibility(t *testing.T) {
	// Existing minimal boot does not include content; migration must still work.
	canvasOperationFixture(t)
	for _, which := range []string{"content", "type", "unrelated"} {
		t.Run(which, func(t *testing.T) {
			f := newManagedFixture(t)
			cs, _ := f.h.contentService()
			item, e := cs.Create(f.ctx, content.Input{Type: "post", Title: "Source", Body: "Original", Status: "draft", AuthorID: f.prepared.OwnerID})
			if e != nil {
				t.Fatal(e)
			}
			card := canvasCard{ID: "44444444-4444-4444-8444-444444444444", Kind: "content", ContentID: item.ID}
			f.board.Cards = append(f.board.Cards, card)
			f.board.Edges = append(f.board.Edges, canvasEdge{card.ID, f.prepared.NodeID})
			if _, e = f.h.d.DB.Exec(`UPDATE admin_canvases SET document_json=?,edges_json=?,version=version+1 WHERE id=?`, canvasEncode(f.board.Cards), canvasEncode(f.board.Edges), f.board.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = f.h.d.DB.Exec(`UPDATE admin_canvas_grants SET node_ids=? WHERE board_id=?`, canvasEncode([]string{f.board.Cards[0].ID, card.ID}), f.board.ID); e != nil {
				t.Fatal(e)
			}
			f.board, e = f.h.canvasOwned(f.ctx, f.board.ID)
			if e != nil {
				t.Fatal(e)
			}
			f.prepared, e = f.h.canvasPrepare(f.ctx, f.board, f.prepared.NodeID)
			if e != nil {
				t.Fatal(e)
			}
			id := f.run(t)
			f.approve(t, id)
			switch which {
			case "content":
				_, e = f.h.d.DB.Exec(`UPDATE content SET body='Changed' WHERE id=?`, item.ID)
				if e == nil {
					_, e = f.h.d.DB.Exec(`UPDATE content SET body='Original' WHERE id=?`, item.ID)
				}
			case "type":
				_, e = f.h.d.DB.Exec(`UPDATE content_types SET public=0 WHERE slug='post'`)
				if e == nil {
					_, e = f.h.d.DB.Exec(`UPDATE content_types SET public=1 WHERE slug='post'`)
				}
			case "unrelated":
				_, e = cs.Create(f.ctx, content.Input{Type: "post", Title: "Unrelated", Body: "Other", Status: "draft", AuthorID: f.prepared.OwnerID})
			}
			if e != nil {
				t.Fatal(e)
			}
			f.work(t, id)
			if which == "unrelated" {
				if f.status(t, id) != "succeeded" || f.toolCalls != 1 {
					t.Fatal("unrelated item invalidated direct source", f.status(t, id))
				}
			} else if f.toolCalls != 0 || f.status(t, id) != "failed" {
				t.Fatal("restored source revived consent")
			}
		})
	}
}
func TestManagedToolsCapabilityURLAndCredentialPrivacy(t *testing.T) {
	f := newManagedFixture(t)
	model, e := f.h.loadAgentModel(f.ctx, f.prepared.KeyID)
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.h.loadConnection(f.ctx, model.ConnectionID)
	if e != nil {
		t.Fatal(e)
	}
	c.Endpoint = "https://api.openai.com/CAPABILITY_PATH/v1"
	c.Secret, e = f.h.encryptConnectionSecrets(connectionSecrets{Token: "PRIVATE_MODEL_TOKEN"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.h.d.Settings.Set(f.ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	f.prepared, e = f.h.canvasPrepare(f.ctx, f.board, f.prepared.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(f.prepared)
	for _, secret := range []string{"CAPABILITY_PATH", "PRIVATE_MODEL_TOKEN", "private-mcp-token", "https://archive.example/mcp"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("prepared leaked credential", secret)
		}
	}
	id := f.run(t)
	f.reply = `{"content":[{"type":"text","text":"Actual archive evidence PRIVATE_MODEL_TOKEN private-mcp-token"}]}`
	f.approve(t, id)
	f.work(t, id)
	var state string
	f.h.d.DB.QueryRow(`SELECT document FROM admin_canvas_tool_states WHERE run_id=?`, id).Scan(&state)
	for _, secret := range []string{"CAPABILITY_PATH", "PRIVATE_MODEL_TOKEN", "private-mcp-token"} {
		if strings.Contains(state, secret) {
			t.Fatal("serialized transcript leaked credential", secret)
		}
	}
	var audit string
	if e = f.h.d.DB.QueryRow(`SELECT COALESCE(group_concat(meta_json),'') FROM audit_log WHERE action LIKE 'canvas.tool.%'`).Scan(&audit); e != nil {
		t.Fatal(e)
	}
	for _, hidden := range []string{"PRIVATE_MODEL_TOKEN", "private-mcp-token", "evidence", "Actual archive", "CAPABILITY_PATH"} {
		if strings.Contains(audit, hidden) {
			t.Fatal("audit leaked private payload", hidden)
		}
	}
}

func TestManagedToolsPolicyEditorDiscoveryDoesNotExecute(t *testing.T) {
	f := newManagedFixture(t)
	f.h.tpl, _ = kernel.Get[*httpx.Templates](f.h.d, kernel.ServiceAdminTemplates)
	editor := func(method string, values url.Values, sendCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/admin/agents/keys/"+f.prepared.KeyID+"/tools", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "gp_session", Value: f.cookie})
		if sendCSRF {
			r.Header.Set("X-CSRF-Token", f.csrf)
		}
		router := chi.NewRouter()
		router.Use(auth.SessionMiddleware(f.h.d), auth.CSRFMiddleware())
		router.With(auth.RequireRecentAuth).MethodFunc(method, "/admin/agents/keys/{id}/tools", func(w http.ResponseWriter, r *http.Request) { f.h.managedToolsEditorUsing(w, r, f.client) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := editor("GET", nil, false); w.Code != 200 || !strings.Contains(w.Body.String(), "data-managed-tools") {
		t.Fatal("editor", w.Code, w.Body.String())
	}
	if f.toolCalls != 0 || f.modelCalls != 0 {
		t.Fatal("GET executed remote work")
	}
	bundle, e := currentAgentBundle(f.ctx, f.h.d.DB, f.prepared.OwnerID, f.prepared.KeyID)
	if e != nil {
		t.Fatal(e)
	}
	form := url.Values{"action": {"discover"}, "version": {bundle.Version}}
	if w := editor("POST", form, false); w.Code != 403 {
		t.Fatal("editor CSRF bypass", w.Code)
	}
	if w := editor("POST", form, true); w.Code != 200 || !strings.Contains(w.Body.String(), `name="allow"`) {
		t.Fatal("discovery", w.Code, w.Body.String())
	}
	if f.toolCalls != 0 || f.modelCalls != 0 {
		t.Fatal("discovery executed action")
	}
	var token string
	if e = f.h.d.DB.QueryRow(`SELECT id FROM admin_agent_tool_catalogs WHERE key_id=?`, f.prepared.KeyID).Scan(&token); e != nil {
		t.Fatal(e)
	}
	form = url.Values{"action": {"save"}, "version": {bundle.Version}, "catalog": {token}, "allow": {f.alias}, "reviewed": {"1"}}
	if w := editor("POST", form, true); w.Code != 303 {
		t.Fatal("save policy", w.Code, w.Body.String())
	}
	saved, e := currentAgentBundle(f.ctx, f.h.d.DB, f.prepared.OwnerID, f.prepared.KeyID)
	if e != nil || saved.Tools == nil || saved.Version == bundle.Version {
		t.Fatal("policy not persisted", e)
	}
	// A policy editor save cannot enable scheduled/incoming execution.
	p, e := f.h.canvasPrepare(f.ctx, f.board, f.prepared.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	p.ScheduleID = f.board.Cards[2].ID
	if _, e = f.h.canvasEnqueue(f.ctx, p, "not-recurring"); e == nil {
		t.Fatal("scheduled tool policy accepted")
	}
	p.ScheduleID = ""
	p.InboundID = db.NewID()
	if _, e = f.h.canvasEnqueue(f.ctx, p, "not-inbound"); e == nil {
		t.Fatal("incoming tool policy accepted")
	}
}

func TestManagedToolsParallelApprovalAndOwnerIsolation(t *testing.T) {
	f := newManagedFixture(t)
	id := f.run(t)
	code, review := f.approval(t, id, "GET", nil, false)
	if code != 200 {
		t.Fatal(code)
	}
	in := map[string]any{"approval_id": review["approval_id"], "revision": review["revision"], "approve": true}
	responses := make(chan int, 2)
	for n := 0; n < 2; n++ {
		go func() { code, _ := f.approval(t, id, "POST", in, true); responses <- code }()
	}
	a, b := <-responses, <-responses
	if !(a == 200 && b == 409 || b == 200 && a == 409) {
		t.Fatal("approval not one-shot", a, b)
	}
	f.work(t, id)
	if f.toolCalls != 1 {
		t.Fatal("parallel approval duplicated action", f.toolCalls)
	}
	other, e := f.h.users.Create(f.ctx, "other-managed@example.com", "Other", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.h.d.DB.Exec(`UPDATE sessions SET user_id=?`, other.ID); e != nil {
		t.Fatal(e)
	}
	if code, _ = f.approval(t, id, "GET", nil, false); code != 404 {
		t.Fatal("other owner read approval", code)
	}
	if code, _ = f.approval(t, id, "POST", in, true); code != 404 {
		t.Fatal("other owner mutated approval", code)
	}
}
func TestManagedToolsEpochAlsoGuardsFinalAppend(t *testing.T) {
	f := newManagedFixture(t)
	r, e := f.h.canvasEnqueue(f.ctx, f.prepared, "final-boundary")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.h.d.DB.Exec(`UPDATE admin_canvas_runs SET status='running' WHERE id=?`, r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_run=0 WHERE board_id=?`, f.board.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_run=1 WHERE board_id=?`, f.board.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.h.canvasAppend(f.ctx, r.ID, f.prepared, "Stale output", "user", "Owner", "running"); e == nil {
		t.Fatal("final append accepted restored authority")
	}
	var n int
	f.h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, r.ID).Scan(&n)
	if n != 0 {
		t.Fatal("stale final output appended")
	}
}
