package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mcp"
)

const layoutPassword = "layout-api-password-123456"

type layoutFixture struct {
	app   *kernel.App
	svc   *service
	cs    content.Service
	srv   *httptest.Server
	users auth.Service
	keys  auth.KeyService
}

func newLayoutFixture(t *testing.T, modules ...kernel.Module) *layoutFixture {
	t.Helper()
	app := apitest.Boot(t, append([]kernel.Module{content.New(), New()}, modules...)...)
	svc, ok := kernel.Get[*service](app.Deps, "builder")
	if !ok {
		t.Fatal("missing builder service")
	}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	return &layoutFixture{app, svc, cs, srv, users, keys}
}
func (f *layoutFixture) user(t *testing.T, role string) *auth.User {
	t.Helper()
	u, err := f.users.Create(context.Background(), role+"@layout.test", role, layoutPassword, role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func (f *layoutFixture) page(t *testing.T, u *auth.User, title string) *content.Item {
	t.Helper()
	it, err := f.cs.Create(auth.WithUser(context.Background(), u), content.Input{Type: "page", Title: title, Body: "<p>Original public body</p>", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	return it
}
func (f *layoutFixture) token(t *testing.T, u *auth.User, scopes []string) string {
	t.Helper()
	token, _, err := f.keys.Create(context.Background(), "layout test", u.ID, scopes, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func (f *layoutFixture) request(t *testing.T, client *http.Client, method, path, token, csrf string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			reader = strings.NewReader(b)
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}
func decodeLayoutResponse(t *testing.T, raw []byte) *Draft {
	t.Helper()
	var envelope struct {
		OK   bool  `json:"ok"`
		Data Draft `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("not OK: %s", raw)
	}
	return &envelope.Data
}
func (f *layoutFixture) session(t *testing.T, u *auth.User, pageID string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrfPattern := regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)
	status, raw := f.request(t, client, "GET", "/admin/login", "", "", nil)
	if status != 200 {
		t.Fatalf("login GET %d %s", status, raw)
	}
	match := csrfPattern.FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatal("missing login CSRF")
	}
	resp, err := client.PostForm(f.srv.URL+"/admin/login", url.Values{"email": {u.Email}, "password": {layoutPassword}, "_csrf": {string(match[1])}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("session login %d", resp.StatusCode)
	}
	status, raw = f.request(t, client, "GET", "/admin/content/page/"+pageID, "", "", nil)
	if status != 200 {
		t.Fatalf("editor GET %d %s", status, raw)
	}
	match = csrfPattern.FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatal("missing session CSRF")
	}
	return client, string(match[1])
}
func TestLayoutRESTSessionAndKeyOwnershipAndScopes(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	author := f.user(t, "author")
	contributor := f.user(t, "contributor")
	own := f.page(t, author, "Author page")
	other := f.page(t, admin, "Admin private page")
	limitedPage := f.page(t, contributor, "Contributor page")
	ctx := auth.WithUser(context.Background(), admin)
	initial, err := f.svc.Load(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := DefaultSection("hero", "en")
	secret.Title = "CONFIDENTIAL LAYOUT"
	if _, err = f.svc.Save(ctx, other.ID, Document{Version: 1, Sections: []Section{secret}}, initial.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	key := f.token(t, author, []string{"content.edit", "content.publish"})
	session, csrf := f.session(t, author, own.ID)
	for _, channel := range []struct {
		name      string
		client    *http.Client
		key, csrf string
	}{{"key", http.DefaultClient, key, ""}, {"session", session, "", csrf}} {
		t.Run(channel.name, func(t *testing.T) {
			status, body := f.request(t, channel.client, "GET", "/api/v1/builder/"+other.ID, channel.key, channel.csrf, nil)
			if status != 403 || bytes.Contains(body, []byte("CONFIDENTIAL LAYOUT")) {
				t.Fatalf("other draft read %d %s", status, body)
			}
			status, body = f.request(t, channel.client, "GET", "/api/v1/builder/"+own.ID, channel.key, channel.csrf, nil)
			if status != 200 {
				t.Fatalf("own read %d %s", status, body)
			}
			draft := decodeLayoutResponse(t, body)
			section := DefaultSection("hero", "en")
			section.Title = "Saved through " + channel.name
			request := saveRequest{Document: Document{Version: 1, Sections: []Section{section}}, BaseUpdatedAt: draft.UpdatedAt}
			status, body = f.request(t, channel.client, "PUT", "/api/v1/builder/"+own.ID+"/draft", channel.key, channel.csrf, request)
			if status != 200 {
				t.Fatalf("save %d %s", status, body)
			}
			saved := decodeLayoutResponse(t, body)
			fresh, err := f.cs.Get(auth.WithUser(context.Background(), author), own.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fresh.Body, section.Title) || fresh.Status != "draft" {
				t.Fatal("private save published content")
			}
			status, _ = f.request(t, channel.client, "PUT", "/api/v1/builder/"+own.ID+"/draft", channel.key, channel.csrf, request)
			if status != 409 {
				t.Fatalf("stale save %d", status)
			}
			status, body = f.request(t, channel.client, "PUT", "/api/v1/builder/"+other.ID+"/draft", channel.key, channel.csrf, saveRequest{Document: request.Document, BaseUpdatedAt: saved.UpdatedAt})
			if status != 403 {
				t.Fatalf("other write %d %s", status, body)
			}
			if channel.name == "session" {
				status, _ = f.request(t, session, "PUT", "/api/v1/builder/"+own.ID+"/draft", "", "", request)
				if status != 403 {
					t.Fatalf("missing CSRF %d", status)
				}
			}
		})
	}
	contributorKey := f.token(t, contributor, []string{"content.edit", "content.publish"})
	draft, err := f.svc.Load(auth.WithUser(context.Background(), contributor), limitedPage.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, body := f.request(t, http.DefaultClient, "POST", "/api/v1/builder/"+limitedPage.ID+"/publish", contributorKey, "", layoutPublishRequest{UpdatedAt: draft.UpdatedAt})
	if status != 403 {
		t.Fatalf("contributor publish %d %s", status, body)
	}
	readonly := f.token(t, author, []string{"content.view"})
	status, _ = f.request(t, http.DefaultClient, "GET", "/api/v1/builder/"+own.ID, readonly, "", nil)
	if status != 403 {
		t.Fatalf("view-only draft read %d", status)
	}
	status, _ = f.request(t, http.DefaultClient, "GET", "/api/v1/builder/"+own.ID, "", "", nil)
	if status != 401 {
		t.Fatalf("anonymous read %d", status)
	}
}
func TestLayoutRESTPublishAndStrictBodies(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	it := f.page(t, admin, "Publish page")
	token := f.token(t, admin, []string{"content.edit", "content.publish"})
	path := "/api/v1/builder/" + it.ID
	status, body := f.request(t, http.DefaultClient, "GET", path, token, "", nil)
	if status != 200 {
		t.Fatalf("get %d %s", status, body)
	}
	draft := decodeLayoutResponse(t, body)
	doc := Document{Version: 1, Sections: []Section{DefaultSection("hero", "en")}}
	for _, raw := range []string{`{"unknown":true}`, `{} {}`} {
		status, body = f.request(t, http.DefaultClient, "PUT", path+"/draft", token, "", raw)
		if status != 400 {
			t.Fatalf("invalid body %d %s", status, body)
		}
	}
	unsafe := doc
	unsafe.Sections = append([]Section(nil), doc.Sections...)
	unsafe.Sections[0].Link = "javascript:alert(1)"
	status, _ = f.request(t, http.DefaultClient, "PUT", path+"/draft", token, "", saveRequest{Document: unsafe, BaseUpdatedAt: draft.UpdatedAt})
	if status != 422 {
		t.Fatalf("unsafe layout %d", status)
	}
	status, body = f.request(t, http.DefaultClient, "PUT", path+"/draft", token, "", saveRequest{Document: doc, BaseUpdatedAt: draft.UpdatedAt})
	if status != 200 {
		t.Fatalf("save %d %s", status, body)
	}
	saved := decodeLayoutResponse(t, body)
	status, body = f.request(t, http.DefaultClient, "POST", path+"/publish", token, "", layoutPublishRequest{UpdatedAt: saved.UpdatedAt})
	if status != 200 {
		t.Fatalf("publish %d %s", status, body)
	}
	published := decodeLayoutResponse(t, body)
	if !published.HasPublished || published.UpdatedAt == saved.UpdatedAt {
		t.Fatal("publish did not return fresh snapshot token")
	}
	fresh, err := f.cs.Get(auth.WithUser(context.Background(), admin), it.ID)
	if err != nil || fresh.Status != "published" || !strings.Contains(fresh.Body, "gp-builder") {
		t.Fatalf("not published: %#v %v", fresh, err)
	}
	status, _ = f.request(t, http.DefaultClient, "POST", path+"/publish", token, "", layoutPublishRequest{UpdatedAt: saved.UpdatedAt})
	if status != 409 {
		t.Fatalf("stale publish %d", status)
	}
}
func TestLayoutMCPRegisteredScopesSensitivityAndHandlers(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	writer := f.user(t, "contributor")
	own := f.page(t, writer, "My draft")
	other := f.page(t, admin, "Other draft")
	tools := map[string]kernel.MCPTool{}
	for _, tool := range f.app.Deps.MCP.Tools() {
		tools[tool.Name] = tool
	}
	get, save, publish := tools["get_page_layout"], tools["save_page_layout"], tools["publish_page_layout"]
	if get.Handler == nil || save.Handler == nil || publish.Handler == nil {
		t.Fatal("layout tools missing")
	}
	if get.Sensitive || save.Sensitive || !publish.Sensitive {
		t.Fatal("incorrect approval flags")
	}
	if strings.Join(publish.Scopes, ",") != "content.edit,content.publish" || strings.Join(save.Scopes, ",") != "content.edit" {
		t.Fatal("incorrect scopes")
	}
	call := func(tool kernel.MCPTool, ctx context.Context, in any) (any, error) {
		raw, _ := json.Marshal(in)
		return tool.Handler(ctx, raw)
	}
	ctx := auth.WithUser(context.Background(), writer)
	if _, err := call(get, ctx, layoutIDInput{ID: other.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other tool read %v", err)
	}
	value, err := call(get, ctx, layoutIDInput{ID: own.ID})
	if err != nil {
		t.Fatal(err)
	}
	draft := value.(*Draft)
	doc := Document{Version: 1, Sections: []Section{DefaultSection("hero", "en")}}
	value, err = call(save, ctx, layoutSaveInput{ID: own.ID, Document: doc, BaseUpdatedAt: draft.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	saved := value.(*Draft)
	if _, err = call(publish, ctx, layoutPublishInput{ID: own.ID, UpdatedAt: saved.UpdatedAt}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("contributor tool publish %v", err)
	}
	if _, err = save.Handler(ctx, json.RawMessage(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown arguments accepted")
	}
	if _, err = save.Handler(ctx, json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("trailing arguments accepted")
	}
	if _, err = save.Handler(ctx, json.RawMessage(strings.Repeat(" ", 1<<20)+`{}`)); err == nil {
		t.Fatal("oversized arguments accepted")
	}
	_, err = call(publish, auth.WithUser(context.Background(), admin), layoutPublishInput{ID: own.ID, UpdatedAt: saved.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
}

type layoutBearer string

func (b layoutBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}
func layoutSDK(t *testing.T, f *layoutFixture, token string) *sdk.ClientSession {
	t.Helper()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "layout-integration", Version: "test"}, nil).Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: f.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1, HTTPClient: &http.Client{Transport: layoutBearer(token)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func layoutSDKCall(t *testing.T, client *sdk.ClientSession, name string, args any) (map[string]any, bool) {
	t.Helper()
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 {
		t.Fatal("no MCP content")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("unexpected MCP content %T", result.Content[0])
	}
	var out map[string]any
	if err = json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatal(err)
	}
	return out, result.IsError
}
func layoutSDKData(t *testing.T, envelope map[string]any, isError bool) map[string]any {
	t.Helper()
	if isError || envelope["ok"] != true {
		t.Fatalf("MCP failure %v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("no MCP data %v", envelope)
	}
	return data
}
func layoutSDKCode(t *testing.T, envelope map[string]any, isError bool) string {
	t.Helper()
	if !isError || envelope["ok"] != false {
		t.Fatalf("expected MCP error %v", envelope)
	}
	body, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("no MCP error %v", envelope)
	}
	code, _ := body["code"].(string)
	return code
}
func TestLayoutMCPRealProtocolOwnershipAndHumanApproval(t *testing.T) {
	f := newLayoutFixture(t, mcp.New())
	admin := f.user(t, "admin")
	author := f.user(t, "author")
	own := f.page(t, author, "Protocol page")
	other := f.page(t, admin, "Hidden page")
	normalToken := f.token(t, author, []string{"content.edit", "content.publish"})
	client := layoutSDK(t, f, normalToken)
	tools, err := client.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"get_page_layout", "save_page_layout", "publish_page_layout"} {
		if !names[name] {
			t.Fatalf("missing SDK tool %s", name)
		}
	}
	envelope, isError := layoutSDKCall(t, client, "get_page_layout", layoutIDInput{ID: other.ID})
	if code := layoutSDKCode(t, envelope, isError); code != "forbidden" {
		t.Fatalf("owner denial %s", code)
	}
	envelope, isError = layoutSDKCall(t, client, "get_page_layout", layoutIDInput{ID: own.ID})
	initial := layoutSDKData(t, envelope, isError)
	token := initial["updated_at"].(string)
	section := DefaultSection("hero", "en")
	section.Title = "Approved public protocol title"
	doc := Document{Version: 1, Sections: []Section{section}}
	envelope, isError = layoutSDKCall(t, client, "save_page_layout", layoutSaveInput{ID: own.ID, Document: doc, BaseUpdatedAt: token})
	saved := layoutSDKData(t, envelope, isError)
	draftToken := saved["updated_at"].(string)
	fresh, err := f.cs.Get(auth.WithUser(context.Background(), author), own.ID)
	if err != nil || fresh.Status != "draft" || strings.Contains(fresh.Body, section.Title) {
		t.Fatal("SDK save changed public state")
	}
	envelope, isError = layoutSDKCall(t, client, "save_page_layout", layoutSaveInput{ID: other.ID, Document: doc, BaseUpdatedAt: token})
	if code := layoutSDKCode(t, envelope, isError); code != "forbidden" {
		t.Fatalf("other save %s", code)
	}
	envelope, isError = layoutSDKCall(t, client, "save_page_layout", layoutSaveInput{ID: own.ID, Document: doc, BaseUpdatedAt: token})
	if code := layoutSDKCode(t, envelope, isError); code != "conflict" {
		t.Fatalf("stale save %s", code)
	}
	envelope, isError = layoutSDKCall(t, client, "publish_page_layout", layoutPublishInput{ID: own.ID, UpdatedAt: draftToken})
	pending := layoutSDKData(t, envelope, isError)
	if pending["status"] != "pending_approval" {
		t.Fatalf("not awaiting approval %v", pending)
	}
	approvalID := pending["approval_id"].(string)
	fresh, err = f.cs.Get(auth.WithUser(context.Background(), author), own.ID)
	if err != nil || fresh.Status != "draft" || strings.Contains(fresh.Body, section.Title) {
		t.Fatal("pending approval published")
	}
	approver := f.token(t, admin, []string{"mcp.approve"})
	status, body := f.request(t, http.DefaultClient, "POST", "/api/v1/mcp/approvals/"+approvalID+"/approve", approver, "", nil)
	if status != 200 {
		t.Fatalf("approve %d %s", status, body)
	}
	fresh, err = f.cs.Get(auth.WithUser(context.Background(), author), own.ID)
	if err != nil || fresh.Status != "published" || !strings.Contains(fresh.Body, section.Title) {
		t.Fatalf("approved layout not published %#v %v", fresh, err)
	}
	reader := layoutSDK(t, f, f.token(t, author, []string{"content.view"}))
	envelope, isError = layoutSDKCall(t, reader, "get_page_layout", layoutIDInput{ID: own.ID})
	if code := layoutSDKCode(t, envelope, isError); code != "forbidden" {
		t.Fatalf("missing edit scope %s", code)
	}
}
func TestLayoutPublicationValidationRESTAndMCP(t *testing.T) {
	f := newLayoutFixture(t, mcp.New())
	admin := f.user(t, "admin")
	it := f.page(t, admin, "Invalid publication")
	token := f.token(t, admin, []string{"content.edit", "content.publish", "mcp.approve_self"})
	client := layoutSDK(t, f, token)
	path := "/api/v1/builder/" + it.ID
	hidden := DefaultSection("hero", "en")
	hidden.Hidden = true
	for _, doc := range []Document{{Version: 1, Sections: []Section{}}, {Version: 1, Sections: []Section{hidden}}} {
		draft, err := f.svc.Load(auth.WithUser(context.Background(), admin), it.ID)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := f.svc.Save(auth.WithUser(context.Background(), admin), it.ID, doc, draft.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		status, body := f.request(t, http.DefaultClient, "POST", path+"/publish", token, "", layoutPublishRequest{UpdatedAt: saved.UpdatedAt})
		if status != 422 {
			t.Fatalf("invalid publish %d %s", status, body)
		}
		envelope, isError := layoutSDKCall(t, client, "publish_page_layout", layoutPublishInput{ID: it.ID, UpdatedAt: saved.UpdatedAt})
		if code := layoutSDKCode(t, envelope, isError); code != "validation_failed" {
			t.Fatalf("invalid MCP publish %s", code)
		}
	}
	fresh, err := f.cs.Get(auth.WithUser(context.Background(), admin), it.ID)
	if err != nil || fresh.Status != "draft" {
		t.Fatal("invalid draft was published")
	}
}
