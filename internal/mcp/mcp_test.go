package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

func TestToolsListMatchesScopes(t *testing.T) {
	e := newEnv(t, &fakeModule{commerce: true, wpmigrate: true})
	reader := e.agent("content.view")
	names := reader.toolNames()
	for _, want := range []string{"list_content_types", "get_content_type", "list_content", "get_content", "list_media",
		"list_taxonomies", "list_terms", "get_site_structure", "check_approval", "fake_sensitive"} {
		if !contains(names, want) {
			t.Errorf("content.view key must see %s; got %v", want, names)
		}
	}
	for _, hidden := range []string{"create_content_type", "create_content", "update_content", "publish_content", "delete_content",
		"upload_media", "create_term", "list_templates", "create_template_draft", "get_logs", "update_settings",
		"run_migration_preview", "list_products"} {
		if contains(names, hidden) {
			t.Errorf("content.view key must not see %s", hidden)
		}
	}
	if errObj := reader.fails("create_content", map[string]any{"type": "post", "title": "x"}); str(errObj, "code") != "forbidden" {
		t.Fatalf("unauthorized call: want forbidden, got %v", errObj)
	}

	full := e.agent("*").toolNames()
	for _, want := range []string{"create_content_type", "update_content_type_fields", "publish_content", "unpublish_content",
		"upload_media", "create_template_draft", "get_logs", "update_settings", "run_migration_preview", "get_migration_status", "list_products"} {
		if !contains(full, want) {
			t.Errorf("* key must see %s", want)
		}
	}
	// Schemas come from the Input structs (snake_case, only real requirements required).
	res, err := e.agent("*").cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name != "create_content" {
			continue
		}
		b, _ := json.Marshal(tl.InputSchema)
		s := string(b)
		if !strings.Contains(s, `"featured_media_id"`) || !strings.Contains(s, `"required":["type","title"]`) {
			t.Fatalf("create_content schema: %s", s)
		}
	}

	// Optional-service tools are absent when the services are not booted.
	bare := newEnv(t, nil).agent("*").toolNames()
	if contains(bare, "list_products") || contains(bare, "run_migration_preview") {
		t.Fatalf("optional tools registered without services: %v", bare)
	}
	if got := e.agent("commerce.manage").ok("list_products", map[string]any{}); got == nil {
		t.Fatal("list_products returned nothing")
	}
}

func TestMCPEndpointRejectsAnonymousAndSessions(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := http.Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), `"jsonrpc":"2.0"`) || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous: %d %s", resp.StatusCode, b)
	}
	e.login("admin") // a session cookie alone is not accepted
	r, page := e.do("POST", "/mcp", nil, "Content-Type", "application/json")
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session: %d %s", r.StatusCode, page)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gp_invalid")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key: %d", resp.StatusCode)
	}
}

func TestSensitiveToolCreatesApprovalAndPendingResult(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view", "content.create", "content.publish")
	item := a.ok("create_content", map[string]any{"type": "post", "title": "Hello", "body": "<p>x</p>"})
	id := str(item, "id")
	if str(item, "status") != "draft" {
		t.Fatalf("create_content must create a draft: %v", item)
	}
	pending := a.ok("publish_content", map[string]any{"id": id})
	if str(pending, "status") != "pending_approval" || str(pending, "approval_id") == "" || str(pending, "expires_at") == "" {
		t.Fatalf("publish_content: %v", pending)
	}
	if n := e.count(`SELECT count(*) FROM mcp_approvals WHERE id = ? AND status = 'pending' AND tool = 'publish_content' AND api_key_id = ?`,
		str(pending, "approval_id"), a.key.Key.ID); n != 1 {
		t.Fatalf("approval row: %d", n)
	}
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if it, _ := svc.Get(context.Background(), id); it.Status != "draft" {
		t.Fatalf("content published before approval: %s", it.Status)
	}
	got := a.ok("check_approval", map[string]any{"approval_id": str(pending, "approval_id")})
	if str(got, "status") != "pending_approval" {
		t.Fatalf("check_approval: %v", got)
	}
	// Another key cannot read this approval.
	other := e.agent("content.view")
	if errObj := other.fails("check_approval", map[string]any{"approval_id": str(pending, "approval_id")}); str(errObj, "code") != "not_found" {
		t.Fatalf("foreign approval: %v", errObj)
	}
	// mcp.approve_self executes immediately.
	self := e.agent("content.view", "content.create", "content.publish", "mcp.approve_self")
	it2 := self.ok("create_content", map[string]any{"type": "post", "title": "Self"})
	if res := self.ok("publish_content", map[string]any{"id": str(it2, "id")}); str(res, "status") != "published" {
		t.Fatalf("approve_self publish: %v", res)
	}
	// The admin page lists the pending call with escaped, pretty args.
	e.login("admin")
	_, page := e.do("GET", "/admin/agents/approvals", nil)
	for _, want := range []string{"publish_content", id, "apitest"} {
		if !strings.Contains(page, want) {
			t.Fatalf("approvals page missing %q", want)
		}
	}
}

func TestApprovedToolExecutesOnceOnly(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	pending := a.ok("fake_sensitive", map[string]any{"note": "<script>alert(1)</script>"})
	aid := str(pending, "approval_id")
	if e.fake.calls.Load() != 0 {
		t.Fatal("sensitive tool ran before approval")
	}
	e.login("admin")
	_, page := e.do("GET", "/admin/agents/approvals", nil)
	if strings.Contains(page, "<script>alert(1)</script>") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("approval args must be HTML-escaped")
	}
	resp, _ := e.adminPost("/admin/agents/approvals/" + aid + "/approve")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %d", resp.StatusCode)
	}
	if e.fake.calls.Load() != 1 {
		t.Fatalf("calls after approve = %d", e.fake.calls.Load())
	}
	e.adminPost("/admin/agents/approvals/" + aid + "/approve")
	api := apitest.NewClient(t, e.app, "mcp.approve")
	if status, _ := api.Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("api approve: %d", status)
	}
	if e.fake.calls.Load() != 1 {
		t.Fatalf("approving twice must be a no-op; calls = %d", e.fake.calls.Load())
	}
	got := a.ok("check_approval", map[string]any{"approval_id": aid})
	result, _ := got["result"].(map[string]any)
	data, _ := result["data"].(map[string]any)
	if str(got, "status") != "approved" || result["ok"] != true || data["executed"] != float64(1) {
		t.Fatalf("check_approval: %v", got)
	}
	// The approved execution runs AS THE KEY (auth.WithAPIKeyActor), not as
	// its owner, and its audit row names the key.
	if data["actor"] != "api_key" {
		t.Fatalf("approved call ran as %v, want api_key", data["actor"])
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND actor_kind = 'api_key' AND actor_id = ? AND json_extract(meta_json, '$.approval_id') = ? AND json_extract(meta_json, '$.executed') = 1`, a.key.Key.ID, aid); n != 1 {
		t.Fatalf("approved call audit must be attributed to the key: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.approval.approve' AND target_id = ?`, aid); n != 1 {
		t.Fatalf("approve audit rows: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND json_extract(meta_json, '$.approval_id') = ? AND json_extract(meta_json, '$.executed') = 1`, aid); n != 1 {
		t.Fatalf("executed call audit rows: %d", n)
	}
	// The real publish flow: approval publishes the content as the key's owner.
	w := e.agent("content.view", "content.create", "content.publish")
	it := w.ok("create_content", map[string]any{"type": "post", "title": "Approved"})
	p := w.ok("publish_content", map[string]any{"id": str(it, "id")})
	if status, _ := api.Post("/v1/mcp/approvals/"+str(p, "approval_id")+"/approve", nil); status != http.StatusOK {
		t.Fatalf("api approve publish: %d", status)
	}
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if got, _ := svc.Get(context.Background(), str(it, "id")); got.Status != "published" {
		t.Fatalf("approved publish: status %s", got.Status)
	}
	// Listing via API.
	if status, body := api.Get("/v1/mcp/approvals"); status != http.StatusOK || body["ok"] != true {
		t.Fatalf("list approvals: %d %v", status, body)
	}
}

func TestDeniedToolReturnsDenied(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	api := apitest.NewClient(t, e.app, "mcp.approve")
	if status, _ := api.Post("/v1/mcp/approvals/"+aid+"/deny", nil); status != http.StatusOK {
		t.Fatalf("deny: %d", status)
	}
	got := a.ok("check_approval", map[string]any{"approval_id": aid})
	if str(got, "status") != "denied" || got["result"] != nil {
		t.Fatalf("check_approval after deny: %v", got)
	}
	api.Post("/v1/mcp/approvals/"+aid+"/approve", nil)
	if e.fake.calls.Load() != 0 {
		t.Fatal("denied tool must never execute")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.approval.deny' AND target_id = ?`, aid); n != 1 {
		t.Fatalf("deny audit rows: %d", n)
	}
	// A key without mcp.approve cannot decide.
	if status, _ := apitest.NewClient(t, e.app, "content.view").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusForbidden {
		t.Fatalf("approve without permission: %d", status)
	}
}

func TestCreateContentTypeViaMCPThenCreateContent(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view", "content.create", "content.edit", "types.manage")
	a.ok("create_content_type", map[string]any{
		"slug": "service", "name": map[string]string{"en": "Services", "ar": "الخدمات"},
		"fields": []map[string]any{{"key": "price", "label": map[string]string{"en": "Price"}, "type": "number", "required": true, "indexed": true}},
	})
	if errObj := a.fails("create_content_type", map[string]any{"slug": "service", "name": map[string]string{"en": "Dup"}}); str(errObj, "code") != "conflict" {
		t.Fatalf("duplicate type: %v", errObj)
	}
	a.ok("update_content_type_fields", map[string]any{"slug": "service", "fields": []map[string]any{
		{"key": "price", "label": map[string]string{"en": "Price"}, "type": "number", "required": true, "indexed": true},
		{"key": "tagline", "label": map[string]string{"en": "Tagline"}, "type": "text"},
	}})
	typ := a.ok("get_content_type", map[string]any{"slug": "service"})
	if fields, _ := typ["fields"].([]any); len(fields) != 2 {
		t.Fatalf("fields after update: %v", typ["fields"])
	}
	if errObj := a.fails("create_content", map[string]any{"type": "service", "title": "No price"}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("missing required field: %v", errObj)
	}
	if errObj := a.fails("create_content", map[string]any{"type": "service", "title": "Live", "status": "published"}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("create_content must not publish: %v", errObj)
	}
	it := a.ok("create_content", map[string]any{"type": "service", "title": "Hosting", "fields": map[string]any{"price": 49, "tagline": "fast"}})
	id := str(it, "id")
	up := a.ok("update_content", map[string]any{"id": id, "title": "Managed hosting"})
	f, _ := up["fields"].(map[string]any)
	if str(up, "title") != "Managed hosting" || f["price"] != float64(49) || f["tagline"] != "fast" {
		t.Fatalf("update_content must merge given keys only: %v", up)
	}
	list := a.ok("list_content", map[string]any{"type": "service", "fields": map[string]any{"price": map[string]any{"op": "gte", "value": 40}}})
	if items, _ := list["items"].([]any); len(items) != 1 {
		t.Fatalf("list_content: %v", list)
	}
	if got := a.ok("get_content", map[string]any{"id": id}); str(got, "title") != "Managed hosting" {
		t.Fatalf("get_content: %v", got)
	}
	site := a.ok("get_site_structure", map[string]any{})
	if types, _ := site["types"].([]any); len(types) < 3 {
		t.Fatalf("site structure types: %v", site["types"])
	}
}

func TestUploadMediaFromURLBlocked(t *testing.T) {
	e := newEnv(t, nil)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG(t))
	}))
	defer internal.Close()
	a := e.agent("media.upload", "content.view")
	errObj := a.fails("upload_media", map[string]any{"url": internal.URL + "/secret.png"})
	if str(errObj, "message") == "" {
		t.Fatalf("blocked URL: %v", errObj)
	}
	if n := e.count(`SELECT count(*) FROM media`); n != 0 {
		t.Fatalf("media rows after blocked URL: %d", n)
	}
	a.fails("upload_media", map[string]any{"filename": "x.png"}) // neither data nor url
	m := a.ok("upload_media", map[string]any{"filename": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(tinyPNG(t)), "alt": "dot"})
	if str(m, "mime") != "image/png" || str(m, "url") == "" {
		t.Fatalf("base64 upload: %v", m)
	}
	big := strings.Repeat("A", maxUploadBase64+8)
	if errObj := a.fails("upload_media", map[string]any{"filename": "big.bin", "data_base64": big}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("oversized upload: %v", errObj)
	}
	list := a.ok("list_media", map[string]any{})
	if items, _ := list["items"].([]any); len(items) != 1 {
		t.Fatalf("list_media: %v", list)
	}
}

func TestCreateTemplateDraftNeverLive(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("themes.manage", "content.view", "mcp.approve_self")
	svc, _ := kernel.Get[theme.Service](e.app.Deps, kernel.ServiceTheme)
	before, err := svc.ReadTemplate(context.Background(), "single.html")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.ok("get_template", map[string]any{"name": "single.html"}); str(got, "body") != before {
		t.Fatal("get_template must return the live body")
	}
	res := a.ok("create_template_draft", map[string]any{"name": "single.html", "body": "AGENT-DRAFT {{.Kind}}"})
	if res["draft"] != true {
		t.Fatalf("create_template_draft: %v", res)
	}
	if after, _ := svc.ReadTemplate(context.Background(), "single.html"); after != before {
		t.Fatal("template draft went live")
	}
	ovs, _ := svc.Overrides(context.Background())
	if len(ovs) != 1 || !ovs[0].Draft {
		t.Fatalf("overrides: %+v", ovs)
	}
	list := a.ok("list_templates", map[string]any{})
	if tpls, _ := list["templates"].([]any); len(tpls) == 0 {
		t.Fatalf("list_templates: %v", list)
	}
	if errObj := a.fails("create_template_draft", map[string]any{"name": "single.html", "body": "{{ nope"}); str(errObj, "message") == "" {
		t.Fatalf("broken template: %v", errObj)
	}
}

func TestAuditRowPerCall(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view", "media.upload")
	a.ok("list_content_types", map[string]any{})
	a.ok("list_content", map[string]any{"type": "post"})
	a.fails("create_content", map[string]any{"type": "post", "title": "x"}) // forbidden is audited too
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t))
	a.ok("upload_media", map[string]any{"filename": "a.png", "data_base64": payload})
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND actor_kind = 'api_key' AND actor_id = ?`, a.key.Key.ID); n != 4 {
		t.Fatalf("mcp.call rows = %d, want 4", n)
	}
	var meta string
	if err := e.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'mcp.call' AND target_kind = 'tool' AND target_id = 'upload_media'`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, payload[:24]) || !strings.Contains(meta, `"args_hash"`) || !strings.Contains(meta, `"duration_ms"`) || !strings.Contains(meta, `"ok":true`) {
		t.Fatalf("upload_media audit meta: %s", meta)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND target_id = 'create_content' AND json_extract(meta_json, '$.ok') = 0`); n != 1 {
		t.Fatalf("forbidden call audit: %d", n)
	}
}

func TestStdioTransportHandshake(t *testing.T) {
	e := newEnv(t, nil)
	key := apitest.NewClient(t, e.app, "content.view")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- run(ctx, e.app, key.Token, &sdk.IOTransport{Reader: sr, Writer: sw}) }()
	c := sdk.NewClient(&sdk.Implementation{Name: "stdio-test", Version: "v0"}, nil)
	cs, err := c.Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if cs.InitializeResult().ServerInfo.Name != "gorun" {
		t.Fatalf("server info: %+v", cs.InitializeResult().ServerInfo)
	}
	ag := &agent{t: t, cs: cs, key: key}
	names := ag.toolNames()
	if !contains(names, "list_content") || contains(names, "create_content") {
		t.Fatalf("stdio tools: %v", names)
	}
	ag.ok("list_content_types", nil)
	res, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "gorun://types"})
	if err != nil || len(res.Contents) != 1 || !strings.Contains(res.Contents[0].Text, `"post"`) {
		t.Fatalf("gorun://types: %v %v", err, res)
	}
	cs.Close()
	cancel()
	<-done
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'mcp.call' AND actor_kind = 'api_key' AND actor_id = ?`, key.Key.ID); n != 1 {
		t.Fatalf("stdio audit rows: %d", n)
	}
	if err := run(context.Background(), e.app, "gp_notavalidkey", &sdk.IOTransport{Reader: io.NopCloser(strings.NewReader("")), Writer: nopWriteCloser{}}); err == nil {
		t.Fatal("invalid key must be rejected")
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func TestTemplateDraftNeedsApproval(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("themes.manage")
	res := a.ok("create_template_draft", map[string]any{"name": "single.html", "body": `<script nonce="{{nonce}}">alert(1)</script>`})
	if str(res, "status") != "pending_approval" {
		t.Fatalf("draft bypassed approval: %v", res)
	}
	svc, _ := kernel.Get[theme.Service](e.app.Deps, kernel.ServiceTheme)
	ovs, err := svc.Overrides(t.Context())
	if err != nil || len(ovs) != 0 {
		t.Fatalf("draft saved before approval: %v %v", ovs, err)
	}
	aid := str(res, "approval_id")
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatal("approval failed")
	}
	ovs, err = svc.Overrides(t.Context())
	if err != nil || len(ovs) != 1 || !ovs[0].Draft {
		t.Fatalf("approved draft missing: %v %v", ovs, err)
	}
}
