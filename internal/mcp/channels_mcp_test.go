package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/workagents"
)

// channelAgent connects an MCP session as guard over app with the given key.
func channelAgent(t *testing.T, app *kernel.App, key *apitest.Client) *agent {
	t.Helper()
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	tr := &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearer{token: key.Token, base: http.DefaultTransport}}}
	c := sdk.NewClient(&sdk.Implementation{Name: "channels-test", Version: "v0"}, nil)
	cs, err := c.Connect(t.Context(), tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return &agent{t: t, cs: cs, key: key}
}

// channelsMCPTest boots settings+auth+api+workagents+mcp and returns the
// owner client, its member agent key and that key's MCP session.
func channelsMCPTest(t *testing.T) (*kernel.App, *apitest.Client, *apitest.Client, *agent) {
	t.Helper()
	app := apitest.Boot(t, workagents.New(nil), New())
	owner := apitest.NewClient(t, app)
	bot := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.read", "workagents.channels"}, User: owner.User, Kind: auth.KeyKindAgent})
	status, env := owner.Post("/workagents/channels", map[string]any{
		"title": "تنسيق المراجعة", "members": []map[string]string{{"kind": "agent_key", "member_id": bot.Key.ID}},
	})
	if status != 201 {
		t.Fatalf("channel create: %d %v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	id, _ := data["id"].(string)
	status, env = owner.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": "الرسالة الأولى من صاحب الحساب"})
	if status != 201 {
		t.Fatalf("owner post: %d %v", status, env)
	}
	return app, owner, bot, channelAgent(t, app, bot)
}

func TestChannelToolsListReadAndPostAsMember(t *testing.T) {
	app, owner, bot, a := channelsMCPTest(t)
	names := a.toolNames()
	for _, want := range []string{"channels_list", "channels_read", "channels_post_message"} {
		if !contains(names, want) {
			t.Fatalf("member key must see %s: %v", want, names)
		}
	}

	list, _ := a.call("channels_list", map[string]any{})
	data, _ := list["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("channels_list: %v", list)
	}
	ch, _ := data[0].(map[string]any)
	id, _ := ch["id"].(string)
	if ch["title"] != "تنسيق المراجعة" || ch["status"] != "open" {
		t.Fatalf("channels_list row: %v", ch)
	}

	read, _ := a.call("channels_read", map[string]any{"channel_id": id})
	rd, _ := read["data"].(map[string]any)
	msgs, _ := rd["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("channels_read: %v", read)
	}

	posted := a.ok("channels_post_message", map[string]any{"channel_id": id, "body": "ملاحظات الوكيل بعد قراءة السياق"})
	if posted["author_kind"] != "agent_key" || posted["author_id"] != bot.Key.ID {
		t.Fatalf("author identity: %v", posted)
	}
	// The advisory hand-off only marks the message; nothing executes.
	if errObj := a.fails("channels_post_message", map[string]any{"channel_id": id, "body": "x", "target_member_id": "unknown-member"}); str(errObj, "code") != "member_not_allowed" {
		t.Fatalf("handoff target: %v", errObj)
	}
	// The message is visible through the REST thread too, ascending.
	status, env := owner.Get("/workagents/channels/" + id + "/messages")
	if status != 200 {
		t.Fatalf("REST thread: %d %v", status, env)
	}
	items, _ := env["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("REST thread length: %v", env)
	}
	last, _ := items[1].(map[string]any)
	if last["body"] != "ملاحظات الوكيل بعد قراءة السياق" {
		t.Fatalf("REST thread body: %v", last)
	}

	// Audit: one appended-message row attributed to the agent key.
	if n := countRows(t, app, `SELECT count(*) FROM audit_log WHERE action='workagent.channel.message' AND actor_kind='api_key' AND actor_id='`+bot.Key.ID+`'`); n != 1 {
		t.Fatalf("channel message audit rows: %d", n)
	}
}

func TestChannelToolsAssistiveScopeGatingAndBoundaries(t *testing.T) {
	app, owner, _, a := channelsMCPTest(t)
	env, isErr := a.call("channels_list", map[string]any{})
	ch, _ := env["data"].([]any)
	if len(ch) != 1 || isErr {
		t.Fatalf("member list: %v", env)
	}
	id, _ := ch[0].(map[string]any)["id"].(string)

	// A non-member key of the same owner can read but not post.
	strangerKey := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.read", "workagents.channels"}, User: owner.User, Kind: auth.KeyKindAgent})
	stranger := channelAgent(t, app, strangerKey)
	if list, isErr := stranger.call("channels_list", map[string]any{}); isErr || list["ok"] != true {
		t.Fatalf("stranger channels_list: %v", list)
	}
	if errObj := stranger.fails("channels_post_message", map[string]any{"channel_id": id, "body": "مش جار"}); str(errObj, "code") != "member_not_allowed" {
		t.Fatalf("non-member post: %v", errObj)
	}
	// Scopes gate visibility: without workagents.read the read/list tools
	// are hidden from tools/list and calls answer forbidden.
	plain := channelAgent(t, app, apitest.NewClient(t, app, "content.view"))
	if names := plain.toolNames(); contains(names, "channels_list") || contains(names, "channels_post_message") {
		t.Fatalf("unscoped key sees channel tools: %v", names)
	}
	if errObj := plain.fails("channels_list", map[string]any{}); str(errObj, "code") != "forbidden" {
		t.Fatalf("unscoped call: %v", errObj)
	}
	// A write-scope-only key sees only the posting tool post.
	writerOnly := channelAgent(t, app, apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.channels"}, User: owner.User, Kind: auth.KeyKindAgent}))
	if names := writerOnly.toolNames(); contains(names, "channels_list") || contains(names, "channels_read") || !contains(names, "channels_post_message") {
		t.Fatalf("write-only visibility: %v", names)
	}
	if errObj := writerOnly.fails("channels_read", map[string]any{"channel_id": id}); str(errObj, "code") != "forbidden" {
		t.Fatalf("write-only read: %v", errObj)
	}

	// Closing hides the channel from channels_list and channel_closed blocks
	// posts; reading stays possible.
	if status, env := owner.Post("/workagents/channels/"+id+"/close", map[string]any{}); status != 200 {
		t.Fatalf("close failed: %d %v", status, env)
	}
	if got, _ := a.call("channels_list", map[string]any{}); got["data"] != nil && strings.Contains(readAll(got), id) {
		t.Fatalf("closed channel still open in list: %v", got)
	}
	if errObj := a.fails("channels_post_message", map[string]any{"channel_id": id, "body": "بعد الإغلاق"}); str(errObj, "code") != "channel_closed" {
		t.Fatalf("post after close: %v", errObj)
	}
	if _, isErr := a.call("channels_read", map[string]any{"channel_id": id}); isErr {
		t.Fatal("read after close")
	}
}

func countRows(t *testing.T, app *kernel.App, sql string) int {
	t.Helper()
	var n int
	if err := app.Deps.DB.QueryRow(sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func readAll(env map[string]any) string {
	b, _ := json.Marshal(env)
	return string(b)
}
