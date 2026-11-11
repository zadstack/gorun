package workagents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// setupKeys wires the API key service (nil audit, nil perms: scopes are not
// pre-validated here) and returns it together with one agent key of u.
func setupKeys(t *testing.T, s *service) auth.KeyService {
	t.Helper()
	keys := auth.NewKeyService(s.d.DB, (*audit.Writer)(nil), nil)
	s.d.Services.Set(kernel.ServiceAPIKeys, keys)
	return keys
}

func newAgentKey(t *testing.T, keys auth.KeyService, ctx context.Context, userID, name string) *auth.APIKey {
	t.Helper()
	_, k, err := keys.CreateKind(ctx, auth.KeyKindAgent, name, userID, []string{"workagents.read", "workagents.channels"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// asAgentKey returns ctx acting as k, exactly like an MCP request.
func asAgentKey(t *testing.T, s *service, ctx context.Context, d *kernel.Deps, k *auth.APIKey) context.Context {
	t.Helper()
	out, err := auth.WithAPIKeyActor(ctx, d, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestChannelsCreateListDetailAndIsolation(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	key := newAgentKey(t, keys, ctx, owner(ctx), "Review bot")

	c, e := s.createChannel(ctx, "مراجعة المحتوى", []ChannelMember{{Kind: "agent_key", MemberID: key.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if c.Status != "open" || len(c.Members) != 2 {
		t.Fatal("channel or members wrong", c.Status, len(c.Members))
	}
	seen := map[string]bool{}
	for _, m := range c.Members {
		seen[m.Kind+"\x00"+m.MemberID] = true
	}
	if !seen["user\x00"+owner(ctx)] || !seen["agent_key\x00"+key.ID] {
		t.Fatal("owner user and agent key must both be members")
	}
	items, total, e := s.channelsPage(ctx, 1, 20)
	if e != nil || total != 1 || len(items) != 1 || items[0].MemberCount != 2 || items[0].Status != "open" {
		t.Fatal("channel list", items, total, e)
	}
	detail, e := s.channelDetail(ctx, c.ID)
	if e != nil || len(detail.Members) != 2 {
		t.Fatal("detail members", detail, e)
	}

	// Another owner sees nothing: not in the list, not read, not writable.
	other := auth.WithUser(context.Background(), &auth.User{ID: "other", Role: auth.RoleCustomer, Status: auth.StatusActive})
	if _, e = s.channelDetail(other, c.ID); !errors.Is(e, ErrChannelNotFound) {
		t.Fatal("cross owner detail", e)
	}
	if list, _, e := s.channelsPage(other, 1, 20); e != nil || len(list) != 0 {
		t.Fatal("cross owner list leaked records", list, e)
	}
	if got, e := s.createChannel(other, "Foreign channel", nil); !errors.Is(e, ErrChannelDenied) || got.ID != "" {
		t.Fatal("cross owner create", e)
	}
	if _, e := s.channelDetail(context.Background(), c.ID); !errors.Is(e, ErrChannelDenied) {
		t.Fatal("anonymous read", e)
	}
}

func TestChannelsRejectInvalidMembersAndTitles(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	cases := []struct {
		name, title string
		members     []ChannelMember
		want        error
	}{
		{"empty title", "  ", nil, ErrChannelInvalid},
		{"long title", strings.Repeat("ع", 121), nil, ErrChannelInvalid},
		{"foreign agent key", "قناة", []ChannelMember{{Kind: "agent_key", MemberID: "not-a-key"}}, ErrChannelMember},
		{"foreign user member", "قناة", []ChannelMember{{Kind: "user", MemberID: "someone-else"}}, ErrChannelInvalid},
		{"unknown kind", "قناة", []ChannelMember{{Kind: "bot", MemberID: "x"}}, ErrChannelInvalid},
	}
	for _, tc := range cases {
		if _, e := s.createChannel(ctx, tc.title, tc.members); !errors.Is(e, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, e)
		}
	}
	revoked := newAgentKey(t, keys, ctx, owner(ctx), "Revoked bot")
	if e := keys.Revoke(ctx, revoked.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.createChannel(ctx, "قناة تكملة", []ChannelMember{{Kind: "agent_key", MemberID: revoked.ID}}); !errors.Is(e, ErrChannelMember) {
		t.Fatal("revoked key accepted", e)
	}
	// A member kind user of the owner is simply deduplicated against the
	// implicit owner-user member (still exactly two members afterwards).
	apiOwn := newAgentKey(t, keys, ctx, owner(ctx), "Second bot")
	c, e := s.createChannel(ctx, "قناة مكرر", []ChannelMember{
		{Kind: "user", MemberID: owner(ctx)}, {Kind: "agent_key", MemberID: apiOwn.ID},
	})
	if e != nil || len(c.Members) != 2 {
		t.Fatal("duplicate member handling", e, len(c.Members))
	}
	var count int
	if err := s.d.DB.QueryRow(`SELECT count(*) FROM workagents_channel_members WHERE channel_id=?`, c.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("owner user duplicated", count, err)
	}
}

func TestChannelsMemberCapTwelve(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	ids := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		ids = append(ids, newAgentKey(t, keys, ctx, owner(ctx), "bot-"+string(rune('a'+i))).ID)
	}
	c, e := s.createChannel(ctx, "Squad", []ChannelMember{})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range ids[:11] {
		if _, e = s.addChannelMember(ctx, c.ID, ChannelMember{Kind: "agent_key", MemberID: id}); e != nil {
			t.Fatal("add member", e)
		}
	}
	if _, e = s.addChannelMember(ctx, c.ID, ChannelMember{Kind: "agent_key", MemberID: ids[11]}); !errors.Is(e, ErrChannelInvalid) {
		t.Fatal("thirteenth member accepted (owner user + 12 keys)", e)
	}
	// Re-adding an existing member stays an idempotent no-op.
	if _, e = s.addChannelMember(ctx, c.ID, ChannelMember{Kind: "agent_key", MemberID: ids[0]}); e != nil {
		t.Fatal("idempotent add", e)
	}
}

func TestChannelsOpenChannelCapTwenty(t *testing.T) {
	s, ctx := setup(t, nil)
	for i := 0; i < channelCap; i++ {
		if _, e := s.createChannel(ctx, "قناة "+strings.Repeat("x", i+1), nil); e != nil {
			t.Fatal("create", i, e)
		}
	}
	if _, e := s.createChannel(ctx, "قناة زائدة", nil); !errors.Is(e, ErrChannelBusy) {
		t.Fatal("21st open channel accepted", e)
	}
	c, _, e := s.channelsPage(ctx, 1, channelCap)
	if e != nil || len(c) != channelCap {
		t.Fatal("cap state", len(c), e)
	}
	if _, e = s.closeChannel(ctx, c[0].ID); e != nil {
		t.Fatal("close for room", e)
	}
	// Closed threads do not count towards the cap; a new one fits again.
	if _, e = s.createChannel(ctx, "قناة زائدة", nil); e != nil {
		t.Fatal("closed channel still reserved a cap slot", e)
	}
}

func TestChannelsMessagesOwnershipAndHandoffAdvisoryOnly(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	key := newAgentKey(t, keys, ctx, owner(ctx), "Handoff bot")
	c, e := s.createChannel(ctx, "تنسيق المهمة", []ChannelMember{{Kind: "agent_key", MemberID: key.ID}})
	if e != nil {
		t.Fatal(e)
	}

	// The owner user posts as kind user.
	m1, e := s.postChannelMessage(ctx, c.ID, "المرحلة الأولى تمت، المطلوب مراجعة النص", "")
	if e != nil || m1.AuthorKind != "user" || m1.AuthorID != owner(ctx) || m1.AuthorName == "" {
		t.Fatal("owner post", m1, e)
	}

	// The member agent key posts as kind agent_key without any hand-off.
	keyCtx := asAgentKey(t, s, ctx, s.d, key)
	m2, e := s.postChannelMessage(keyCtx, c.ID, "قريب، سأرسل الملاحظات بعد فحص الروابط", "")
	if e != nil || m2.AuthorKind != "agent_key" || m2.AuthorID != key.ID {
		t.Fatal("agent post", m2, e)
	}

	// An advisory hand-off is recorded in meta and in the audit flag, but it
	// starts no job, writes nothing else and executes nothing.
	m3, e := s.postChannelMessage(keyCtx, c.ID, "يرجى متابعة تحقق النص", key.ID)
	if e != nil {
		t.Fatal("handoff post", e)
	}
	var h *ChannelHandoff = channelHandoff(m3)
	if h == nil || h.TargetMemberID != key.ID || h.TargetMemberKind != "agent_key" {
		t.Fatal("handoff meta", string(m3.Meta))
	}
	if h := channelHandoff(m1); h != nil {
		t.Fatal("plain message must carry no handoff")
	}

	msgs, total, e := s.channelMessages(ctx, c.ID, "", 1, 20)
	if e != nil || total != 3 || len(msgs) != 3 {
		t.Fatal("thread read", total, e)
	}
	if !(msgs[0].CreatedAt <= msgs[2].CreatedAt) || msgs[0].ID == msgs[2].ID {
		t.Fatal("ascending order", msgs[0].CreatedAt, msgs[2].CreatedAt, msgs[0].ID, msgs[2].ID)
	}
	// The after cursor returns only what follows it.
	after, total, e := s.channelMessages(ctx, c.ID, msgs[1].ID, 1, 20)
	if e != nil || total != 1 || len(after) != 1 || after[0].ID != msgs[2].ID {
		t.Fatal("after cursor", after, total, e)
	}
	if _, _, e = s.channelMessages(ctx, c.ID, "no-such-message", 1, 20); !errors.Is(e, ErrChannelInvalid) {
		t.Fatal("foreign cursor", e)
	}

	// Posting requires membership; the owner never added this second key.
	second := newAgentKey(t, keys, ctx, owner(ctx), "Bystander bot")
	otherCtx := asAgentKey(t, s, ctx, s.d, second)
	if _, e = s.postChannelMessage(otherCtx, c.ID, "أنا لست عضوًا", ""); !errors.Is(e, ErrChannelMember) {
		t.Fatal("non-member post", e)
	}
	if _, e = s.postChannelMessage(ctx, c.ID, "", "x"); !errors.Is(e, ErrChannelInvalid) {
		t.Fatal("empty body", e)
	}
	if _, e = s.postChannelMessage(ctx, c.ID, strings.Repeat("ع", channelMessageCapBytes+1), ""); !errors.Is(e, ErrChannelInvalid) {
		t.Fatal("oversized body", e)
	}
	if got, total, e := s.channelMessages(ctx, c.ID, "", 1, 20); e != nil || total != 3 || len(got) != 3 {
		t.Fatal("rejected posts mutated the thread", total, e)
	}
	if h := channelHandoff(m1); h != nil {
		t.Fatal("plain message must carry no handoff", string(m1.Meta))
	}

	// A hand-off target that is not a member is refused.
	stranger := newAgentKey(t, keys, ctx, owner(ctx), "Nonmember bot")
	if _, e = s.postChannelMessage(ctx, c.ID, "تحويل إلى جهة غريبة", stranger.ID); !errors.Is(e, ErrChannelMember) {
		t.Fatal("handoff to non-member", e)
	}
}

func TestChannelsCloseRequiresManageAndBlocksWrites(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	key := newAgentKey(t, keys, ctx, owner(ctx), "Closer bot")
	c, e := s.createChannel(ctx, "قناة سريعة", []ChannelMember{{Kind: "agent_key", MemberID: key.ID}})
	if e != nil {
		t.Fatal(e)
	}
	keyCtx := asAgentKey(t, s, ctx, s.d, key)
	// An agent key path cannot close channels even as a member.
	if _, e = s.closeChannel(keyCtx, c.ID); !errors.Is(e, ErrChannelDenied) {
		t.Fatal("agent key closed a channel", e)
	}
	if _, e = s.closeChannel(ctx, c.ID); e != nil {
		t.Fatal("close", e)
	}
	if _, e = s.closeChannel(ctx, c.ID); e != nil {
		t.Fatal("second close not idempotent", e)
	}
	if got, e := s.channelDetail(ctx, c.ID); e != nil || got.Status != "closed" {
		t.Fatal("status after close", got.Status, e)
	}
	if _, e = s.postChannelMessage(ctx, c.ID, "مرسل بعد الإغلاق", ""); !errors.Is(e, ErrChannelClosed) {
		t.Fatal("message after close", e)
	}
	if _, e = s.postChannelMessage(keyCtx, c.ID, "رسالة بعد الإغلاق", ""); !errors.Is(e, ErrChannelClosed) {
		t.Fatal("agent message after close", e)
	}
	if _, e = s.addChannelMember(ctx, c.ID, ChannelMember{Kind: "agent_key", MemberID: key.ID}); !errors.Is(e, ErrChannelClosed) {
		t.Fatal("member after close", e)
	}
	if _, _, e = s.channelMessages(ctx, c.ID, "", 1, 20); e != nil {
		t.Fatal("reads after close must keep working", e)
	}
}

func TestChannelsAuditEventsAndMetaBounds(t *testing.T) {
	s, ctx := setup(t, nil)
	if e := db.Migrate(ctx, s.d.DB, "audit", audit.Migrations()); e != nil {
		t.Fatal(e)
	}
	s.d.Audit = audit.New(s.d.DB, s.d.Log)
	keys := setupKeys(t, s)
	key := newAgentKey(t, keys, ctx, owner(ctx), "Audited bot")
	c, e := s.createChannel(ctx, "تدقيق", []ChannelMember{{Kind: "agent_key", MemberID: key.ID}})
	if e != nil {
		t.Fatal(e)
	}
	body := "نص سري للتحقق من عدم نسخه إلى سجل التدقيق <script>alert(1)</script>"
	if _, e = s.postChannelMessage(ctx, c.ID, body, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.postChannelMessage(ctx, c.ID, body, key.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.closeChannel(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	rows, e := s.d.DB.Query(`SELECT action, meta_json FROM audit_log ORDER BY at`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var action, meta string
		if e = rows.Scan(&action, &meta); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(meta, body) || strings.Contains(meta, "<script>") {
			t.Fatal("audit copied message body", action, meta)
		}
		counts[action]++
	}
	for _, action := range []string{"workagent.channel.created", "workagent.channel.closed"} {
		if counts[action] != 1 {
			t.Fatalf("%s rows = %d", action, counts[action])
		}
	}
	if counts["workagent.channel.message"] != 2 {
		t.Fatalf("message rows = %d", counts["workagent.channel.message"])
	}
	var withHandoff int
	if e := s.d.DB.QueryRow(`SELECT count(*) FROM workagents_channel_messages WHERE channel_id=? AND meta_json LIKE '%handoff%'`, c.ID).Scan(&withHandoff); e != nil || withHandoff != 1 {
		t.Fatal("handoff rows", withHandoff, e)
	}
}

func TestChannelFailureMapping(t *testing.T) {
	h := &handlers{}
	for _, tc := range []struct {
		err  error
		code int
		text string
	}{
		{ErrChannelBusy, 429, "channel limit reached"},
		{ErrChannelInvalid, 422, "channel_input_invalid"},
		{ErrChannelNotFound, 404, "channel not found"},
		{ErrChannelClosed, 422, "channel_closed"},
		{ErrChannelMember, 422, "member_not_allowed"},
		{ErrChannelDenied, 403, "channel permission denied"},
	} {
		rec := httptest.NewRecorder()
		h.failure(rec, tc.err)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.text) {
			t.Fatal("mapping", tc.err, rec.Code, rec.Body.String())
		}
	}
	if rec := httptest.NewRecorder(); func() int { h.failure(rec, ErrChannelDenied); return rec.Code }() != http.StatusForbidden {
		t.Fatal("denied must be forbidden")
	}
}

func TestChannelsBridgeSurface(t *testing.T) {
	s, ctx := setup(t, nil)
	keys := setupKeys(t, s)
	key := newAgentKey(t, keys, ctx, owner(ctx), "Bridge bot")
	c, e := s.createChannel(ctx, "جسر", []ChannelMember{{Kind: "agent_key", MemberID: key.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.postChannelMessage(ctx, c.ID, "رسالة المالك", ""); e != nil {
		t.Fatal(e)
	}
	bridge := &Channels{s: s}
	keyCtx := asAgentKey(t, s, ctx, s.d, key)
	open, e := bridge.List(keyCtx)
	if e != nil || len(open) != 1 || open[0].ID != c.ID || open[0].Status != "open" {
		t.Fatal("bridge list", open, e)
	}
	msgs, e := bridge.Messages(keyCtx, c.ID, "", 5)
	if e != nil || len(msgs) != 1 {
		t.Fatal("bridge messages", msgs, e)
	}
	posted, e := bridge.Post(keyCtx, c.ID, "رسالة الجسر", "")
	if e != nil || posted.AuthorKind != "agent_key" {
		t.Fatal("bridge post", posted, e)
	}
	// Anonymous/system context is refused at the bridge too.
	if _, e = bridge.List(context.Background()); !errors.Is(e, ErrChannelDenied) {
		t.Fatal("anonymous bridge read", e)
	}
	var now string
	if err := s.d.DB.QueryRow(`SELECT created_at FROM workagents_channel_messages WHERE id=?`, posted.ID).Scan(&now); err != nil {
		t.Fatal(err)
	}
	if _, e = db.ParseTime(now); e != nil {
		t.Fatal("timestamp format", now)
	}
}

// The admin screen renders the Arabic-first catalog labels, the thread lists
// stored messages and reflects the closed state.
func TestChannelsAdminScreenArabicListAndThread(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	c, e := s.createChannel(ctx, "قناة تحقق أخبار", []ChannelMember{})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.postChannelMessage(ctx, c.ID, "بوت التحقق أرسل ملاحظات أولية للمراجعة البشرية", "")
	if e != nil {
		t.Fatal(e)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		return rec
	}
	rec := get("/admin/workagents/channels")
	if rec.Code != 200 {
		t.Fatal("channels list", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"قنوات الوكلاء", "قناة تحقق أخبار"} {
		if !strings.Contains(body, want) {
			t.Fatalf("list page lost %q", want)
		}
	}
	rec = get("/admin/workagents/channels/" + c.ID)
	if rec.Code != 200 {
		t.Fatal("thread page", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, want := range []string{"الرسائل", m.Body, "data-message-id", "مستخدم"} {
		if !strings.Contains(body, want) {
			t.Fatalf("thread page lost %q", want)
		}
	}
	// Timestamps stay left to right inside the Arabic layout, in the shared
	// minute format rather than raw RFC 3339.
	stamp := regexp.MustCompile(`<time datetime="[^"]+" dir="ltr" title="[^"]+ UTC">\d{4}-\d{2}-\d{2} \d{2}:\d{2}</time>`)
	if !stamp.MatchString(body) {
		t.Fatal("thread timestamp is not a left-to-right formatted <time>")
	}
	if list := get("/admin/workagents/channels").Body.String(); !stamp.MatchString(list) {
		t.Fatal("list last activity is not a left-to-right formatted <time>")
	}
	// The human post box is offered for the owner user.
	if !strings.Contains(body, "data-channel-post") {
		t.Fatal("post box missing for owner")
	}
	// Closed channels render the closed note and drop the post box.
	if _, e = s.closeChannel(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	rec = get("/admin/workagents/channels/" + c.ID)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "مغلقة") || strings.Contains(rec.Body.String(), "data-channel-post") {
		t.Fatal("closed thread state", rec.Code)
	}
}

// The thread page picks new members from the owner's agent keys by name and
// keeps raw member IDs out of the visible text.
func TestChannelThreadMemberPickerUsesKeyNames(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	keys := setupKeys(t, s)
	u := auth.UserFromContext(ctx)
	joined := newAgentKey(t, keys, ctx, u.ID, "Campaign assistant")
	spare := newAgentKey(t, keys, ctx, u.ID, "Research assistant")
	c, e := s.createChannel(ctx, "Spring launch", []ChannelMember{{Kind: "agent_key", MemberID: joined.ID}})
	if e != nil {
		t.Fatal(e)
	}
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/workagents/channels/"+c.ID, nil).WithContext(ctx))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatal("thread page", rec.Code)
	}
	picker := regexp.MustCompile(`<select name="member_id" required>(.*?)</select>`).FindStringSubmatch(body)
	if picker == nil {
		t.Fatal("member picker is not a select")
	}
	if !strings.Contains(picker[1], `<option value="`+spare.ID+`">Research assistant</option>`) || strings.Contains(picker[1], joined.ID) {
		t.Fatalf("member picker must offer only non-member keys by name: %s", picker[1])
	}
	if !strings.Contains(body, `<input type="hidden" name="kind" value="agent_key">`) || strings.Contains(body, `<input name="member_id"`) {
		t.Fatal("free-text member ID input is still rendered")
	}
	if !strings.Contains(body, "<bdi>Campaign assistant</bdi>") {
		t.Fatal("member names are not isolated with <bdi>")
	}
	if !strings.Contains(body, `<code dir="ltr">`+joined.ID+`</code>`) || strings.Contains(body, "· "+joined.ID) {
		t.Fatal("member ID must sit in the muted ID detail only")
	}
}
