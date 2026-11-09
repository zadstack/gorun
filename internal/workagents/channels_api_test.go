package workagents

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func channelErrorMessage(env map[string]any) string {
	if e, ok := env["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	return ""
}

func countChannelAudit(t *testing.T, app *kernel.App) (created, messages, closed int) {
	t.Helper()
	for _, q := range []struct {
		sql string
		out *int
	}{
		{`SELECT count(*) FROM audit_log WHERE action='workagent.channel.created'`, &created},
		{`SELECT count(*) FROM audit_log WHERE action='workagent.channel.message'`, &messages},
		{`SELECT count(*) FROM audit_log WHERE action='workagent.channel.closed'`, &closed},
	} {
		if err := app.Deps.DB.QueryRow(q.sql).Scan(q.out); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return created, messages, closed
}

func TestChannelsRESTLifecycleScopeAndBoundaries(t *testing.T) {
	app := apitest.Boot(t, New(nil))
	client := apitest.NewClient(t, app)

	// The owner's own agent key joins as a member (agents themselves use
	// /mcp only: the same key below is refused on /api).
	bot := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.channels", "workagents.read"}, User: client.User, Kind: auth.KeyKindAgent})

	status, env := client.Post("/workagents/channels", map[string]any{
		"title": "مراجعة النسخة", "members": []map[string]string{{"kind": "agent_key", "member_id": bot.Key.ID}},
	})
	if status != 201 {
		t.Fatalf("create: %d %v", status, env)
	}
	id, _ := apitest.Data(env)["id"].(string)
	if id == "" {
		t.Fatal("no channel id", env)
	}
	status, env = client.Get("/workagents/channels/" + id)
	if status != 200 || len(apitest.Data(env)["members"].([]any)) != 2 {
		t.Fatalf("detail: %d %v", status, env)
	}
	status, env = client.Get("/workagents/channels")
	if status != 200 || len(env["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", status, env)
	}

	agentAPI := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.channels"}, User: client.User, Kind: auth.KeyKindAgent})
	if status, body := agentAPI.Get("/workagents/channels"); status != 403 || apitest.ErrorCode(body) != "agent_key_not_allowed" {
		t.Fatalf("agent key on /api: %d %v", status, body)
	}

	// A read-only key cannot write channels (same owner, narrower scopes).
	reader := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.read"}, User: client.User})
	if status, body := reader.Post("/workagents/channels", map[string]any{"title": "X"}); status != 403 {
		t.Fatalf("missing write scope: %d %v", status, body)
	}
	if status, _ := reader.Get("/workagents/channels"); status != 200 {
		t.Fatalf("read scope list: %d", status)
	}
	// A writer without manage cannot close or add members.
	writer := apitest.New(t, app, apitest.Options{Scopes: []string{"workagents.channels", "workagents.read"}, User: client.User})
	if status, _ := writer.Post("/workagents/channels/"+id+"/close", map[string]any{}); status != 403 {
		t.Fatalf("close without manage: %d", status)
	}
	if status, _ := writer.Post("/workagents/channels/"+id+"/members", map[string]any{"kind": "agent_key", "member_id": bot.Key.ID}); status != 403 {
		t.Fatalf("add member without manage: %d", status)
	}

	// Posting works for a member with the write scope…
	status, msg := writer.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": "الرسالة الأولى من المالك"})
	if status != 201 || apitest.Data(msg)["author_kind"] != "user" {
		t.Fatalf("post: %d %v", status, msg)
	}
	firstID, _ := apitest.Data(msg)["id"].(string)
	// …an advisory hand-off target must be a member…
	status, msg = writer.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": "تابع مع فلان", "target_member_id": bot.Key.ID})
	if status != 201 {
		t.Fatalf("handoff post: %d %v", status, msg)
	}
	secondID, _ := apitest.Data(msg)["id"].(string)
	// …and unknown targets are refused with member_not_allowed / 422.
	if status, body := writer.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": "x", "target_member_id": "no-such-member"}); status != 422 || !strings.Contains(channelErrorMessage(body), "member_not_allowed") {
		t.Fatalf("bad handoff target: %d %v", status, body)
	}
	if firstID == secondID {
		t.Fatal("ids must be distinct")
	}

	// Message reads are ascending with an after cursor.
	status, list := client.Get("/workagents/channels/" + id + "/messages")
	if status != 200 || len(list["data"].([]any)) != 2 {
		t.Fatalf("messages: %d %v", status, list)
	}
	status, list = client.Get("/workagents/channels/" + id + "/messages?per_page=1&after=" + firstID)
	if status != 200 {
		t.Fatalf("cursor page: %d %v", status, list)
	}
	if after, _ := list["data"].([]any); len(after) != 1 || list["meta"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("after cursor: %v", apitest.Data(list))
	}

	// Owner scope cap: 20 open channels then 429.
	for i := 0; i < channelCap-1; i++ {
		if status, _ = client.Post("/workagents/channels", map[string]any{"title": "قناة إضافية " + string(rune('أ'+i))}); status != 201 && status != 422 {
			t.Fatalf("cap fill %d: %d", i, status)
		}
	}
	if status, body := client.Post("/workagents/channels", map[string]any{"title": "قناة زائدة"}); status != 429 {
		t.Fatalf("21st channel: %d %v", status, body)
	}

	// Validation: bad titles, unknown member kinds, oversized bodies.
	for _, tc := range []map[string]any{
		{"title": " "},
		{"title": strings.Repeat("ع", 121)},
		{"title": "قناة صالحة", "members": []map[string]string{{"kind": "user", "member_id": "someone"}}},
		{"title": "قناة أخرى", "members": []map[string]string{{"kind": "ghost", "member_id": "x"}}},
	} {
		if status, _ = client.Post("/workagents/channels", tc); status != 422 {
			t.Fatalf("invalid create accepted: %d %v", status, tc)
		}
	}
	if status, body := client.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": strings.Repeat("ع", channelMessageCapBytes+1)}); status != 422 {
		t.Fatalf("oversized body: %d %v", status, body)
	}
	if status, body := client.Post("/workagents/channels", `{"unknown":true}`); status != 422 || channelErrorMessage(body) == "" {
		t.Fatalf("unknown field: %d %v", status, body)
	}

	// Closing needs manage; closed threads deny writes but reads stay open.
	if status, closed := client.Post("/workagents/channels/"+id+"/close", map[string]any{}); status != 200 || closed["ok"] != true {
		t.Fatalf("close: %d %v", status, closed)
	}
	if status, body := client.Post("/workagents/channels/"+id+"/messages", map[string]any{"body": "بعد الإغلاق"}); status != 422 || !strings.Contains(channelErrorMessage(body), "channel_closed") {
		t.Fatalf("post after close: %d %v", status, body)
	}
	if status, _ = client.Get("/workagents/channels/" + id + "/messages"); status != 200 {
		t.Fatalf("read after close: %d", status)
	}

	// Cross-owner: another account sees an empty list and 404s this channel.
	foreign := apitest.NewClient(t, app, "workagents.channels", "workagents.read")
	if status, list := foreign.Get("/workagents/channels"); status != 200 || len(list["data"].([]any)) != 0 {
		t.Fatalf("foreign list: %d %v", status, apitest.Data(list))
	}
	if status, _ = foreign.Get("/workagents/channels/" + id); status != 404 {
		t.Fatalf("foreign detail: %d", status)
	}

	// Audit rows landed for the lifecycle events through the API too.
	created, messages, closed := countChannelAudit(t, app)
	if created < 1 || messages != 2 || closed != 1 {
		t.Fatalf("audit rows: created=%d message=%d closed=%d", created, messages, closed)
	}
}
