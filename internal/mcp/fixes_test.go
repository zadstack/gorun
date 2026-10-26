package mcp

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/seo"
)

func (e *env) contentStatus(id string) content.Status {
	e.t.Helper()
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it, err := svc.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it.Status
}

func (e *env) approvalStatus(id string) string {
	e.t.Helper()
	s, _ := kernel.Get[*server](e.app.Deps, serviceName)
	a, err := s.approvals.get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return a.Status
}

func TestDecisionRequiresSessionOrExplicitApproveScope(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	wild := apitest.NewClient(t, e.app, "*")
	for _, action := range []string{"approve", "deny"} {
		if status, _ := wild.Post("/v1/mcp/approvals/"+aid+"/"+action, nil); status != http.StatusForbidden {
			t.Fatalf("* key %s: %d", action, status)
		}
	}
	if e.fake.calls.Load() != 0 || e.approvalStatus(aid) != StatusPending {
		t.Fatal("a * key decided an approval")
	}
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("explicit mcp.approve key: %d", status)
	}
	aid2 := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	e.login("admin")
	if resp, _ := e.adminPost("/admin/agents/approvals/" + aid2 + "/approve"); resp.StatusCode != http.StatusOK {
		t.Fatalf("session admin: %d", resp.StatusCode)
	}
	if e.fake.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", e.fake.calls.Load())
	}
	// Deciding again reports that nothing changed.
	if _, page := e.adminPost("/admin/agents/approvals/" + aid2 + "/deny"); !strings.Contains(page, "already decided") && !strings.Contains(page, "سبق اتخاذ قرار") {
		t.Fatal("second decision must say it was already decided")
	}
}

func TestStdioRevokedKeyMidSession(t *testing.T) {
	e := newEnv(t, nil)
	key := apitest.NewClient(t, e.app, "content.view")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- run(ctx, e.app, key.Token, &sdk.IOTransport{Reader: sr, Writer: sw}) }()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "v0"}, nil).Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent{t: t, cs: cs, key: key}
	ag.ok("list_content_types", nil)
	keys, _ := kernel.Get[auth.KeyService](e.app.Deps, kernel.ServiceAPIKeys)
	if err := keys.Revoke(context.Background(), key.Key.ID); err != nil {
		t.Fatal(err)
	}
	if errObj := ag.fails("list_content_types", nil); str(errObj, "code") != "unauthorized" {
		t.Fatalf("call after revoke: %v", errObj)
	}
	cs.Close()
	cancel()
	<-done
}

func TestUpdateSettingsValidation(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("settings.manage", "mcp.approve_self")
	get := func(k string) string { return e.app.Deps.Settings.GetString(context.Background(), k, "") }
	res := a.ok("update_settings", map[string]any{"settings": map[string]any{"site.title": "  ZAD Stack ", "seo.title_template": "{title} | ZAD"}})
	if get("site.title") != "ZAD Stack" || get("seo.title_template") != "{title} | ZAD" || res["updated"] == nil {
		t.Fatalf("allowlisted keys: %v / %q", res, get("site.title"))
	}
	cases := map[string]struct {
		settings map[string]any
		key      string
		code     string
	}{
		"excluded":  {map[string]any{"site.base_url": "https://evil.example"}, "site.base_url", "admin_only"},
		"theme":     {map[string]any{"site.theme": "x"}, "site.theme", "admin_only"},
		"invented":  {map[string]any{"site.secret_flag": "1"}, "site.secret_flag", "not_allowed"},
		"bad type":  {map[string]any{"site.title": 42}, "site.title", "must_be_string"},
		"bad value": {map[string]any{"seo.default_og_image": "javascript:alert(1)"}, "seo.default_og_image", "seo.error.image"},
		"partial":   {map[string]any{"site.title": "Changed", "site.locale": "xx"}, "site.locale", "admin.users.error_locale"},
		"local tz":  {map[string]any{"site.timezone": "Local"}, "site.timezone", "admin.settings.error_timezone"},
	}
	for name, c := range cases {
		errObj := a.fails("update_settings", map[string]any{"settings": c.settings})
		fields, _ := errObj["fields"].(map[string]any)
		if str(errObj, "code") != "validation_failed" || fields[c.key] != c.code {
			t.Fatalf("%s: %v", name, errObj)
		}
	}
	if get("site.title") != "ZAD Stack" || get("site.base_url") != "" {
		t.Fatalf("a rejected call changed settings: title=%q base_url=%q", get("site.title"), get("site.base_url"))
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'settings.update'`); n != 1 {
		t.Fatalf("settings.update audit rows: %d", n)
	}
}

func TestGetLogs(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("audit.read", "content.view")
	a.ok("list_content_types", nil)
	logs := a.ok("get_logs", map[string]any{"action": "mcp.call", "limit": 5})
	entries, _ := logs["audit"].([]any)
	if len(entries) == 0 {
		t.Fatalf("get_logs audit: %v", logs)
	}
	if _, ok := logs["failed_jobs"].([]any); !ok {
		t.Fatalf("get_logs failed_jobs: %v", logs)
	}
}

func TestUnpublishAndDeleteApprovalFlow(t *testing.T) {
	e := newEnv(t, nil)
	self := e.agent("content.view", "content.create", "content.publish", "mcp.approve_self")
	id := str(self.ok("create_content", map[string]any{"type": "post", "title": "Live"}), "id")
	self.ok("publish_content", map[string]any{"id": id})
	a := e.agent("content.view", "content.publish", "content.delete")
	approver := apitest.NewClient(t, e.app, "mcp.approve")

	aid := str(a.ok("unpublish_content", map[string]any{"id": id}), "approval_id")
	if e.contentStatus(id) != "published" {
		t.Fatal("unpublished before approval")
	}
	approver.Post("/v1/mcp/approvals/"+aid+"/approve", nil)
	if e.contentStatus(id) != "draft" {
		t.Fatalf("after approved unpublish: %s", e.contentStatus(id))
	}
	did := str(a.ok("delete_content", map[string]any{"id": id}), "approval_id")
	if e.contentStatus(id) != "draft" {
		t.Fatal("deleted before approval")
	}
	approver.Post("/v1/mcp/approvals/"+did+"/approve", nil)
	if e.contentStatus(id) != "trash" {
		t.Fatalf("after approved delete: %s", e.contentStatus(id))
	}
}

func TestApproveAfterKeyRevokedFails(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	keys, _ := kernel.Get[auth.KeyService](e.app.Deps, kernel.ServiceAPIKeys)
	if err := keys.Revoke(context.Background(), a.key.Key.ID); err != nil {
		t.Fatal(err)
	}
	_, body := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil)
	data, _ := body["data"].(map[string]any)
	if str(data, "status") != StatusFailed || !strings.Contains(string(mustJSON(t, data["result"])), "revoked") {
		t.Fatalf("approval after revoke: %v", body)
	}
	if e.fake.calls.Load() != 0 {
		t.Fatal("tool ran for a revoked key")
	}
}

func TestConcurrentApprovalsExecuteOnce(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	users, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	admin, err := users.Create(context.Background(), "approver@example.com", "Approver", testPassword, "admin")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := kernel.Get[*server](e.app.Deps, serviceName)
	ctx := auth.WithUser(context.Background(), admin)
	var wg sync.WaitGroup
	wins := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, won, err := s.approvals.Approve(ctx, aid)
			if err != nil {
				t.Error(err)
			}
			wins <- won
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for w := range wins {
		if w {
			n++
		}
	}
	if n != 1 || e.fake.calls.Load() != 1 {
		t.Fatalf("winners = %d, executions = %d", n, e.fake.calls.Load())
	}
}

func TestUpdateContentOnLiveItemNeedsApproval(t *testing.T) {
	e := newEnv(t, nil)
	self := e.agent("content.view", "content.create", "content.edit", "content.publish", "mcp.approve_self")
	a := e.agent("content.view", "content.create", "content.edit")
	draft := str(a.ok("create_content", map[string]any{"type": "post", "title": "Draft"}), "id")
	if got := a.ok("update_content", map[string]any{"id": draft, "title": "Draft 2", "status": ""}); str(got, "title") != "Draft 2" || str(got, "status") != "draft" {
		t.Fatalf("draft update must apply directly and keep status: %v", got)
	}
	live := str(self.ok("create_content", map[string]any{"type": "post", "title": "Live"}), "id")
	self.ok("publish_content", map[string]any{"id": live})
	p := a.ok("update_content", map[string]any{"id": live, "title": "Live edited"})
	if str(p, "status") != "pending_approval" {
		t.Fatalf("live update must park: %v", p)
	}
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if it, _ := svc.Get(context.Background(), live); it.Title != "Live" {
		t.Fatal("live item changed before approval")
	}
	apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+str(p, "approval_id")+"/approve", nil)
	if it, _ := svc.Get(context.Background(), live); it.Title != "Live edited" || it.Status != "published" {
		t.Fatalf("approved live update: %q %s", it.Title, it.Status)
	}
	// Status changes on a live item are refused even when self-approved.
	if errObj := self.fails("update_content", map[string]any{"id": live, "status": "draft"}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("status change on published item: %v", errObj)
	}
}

func TestParkedArgsAreValidated(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	a.fails("fake_sensitive", map[string]any{"unknown": 1})
	a.fails("fake_sensitive", map[string]any{"note": strings.Repeat("x", maxParkedArgs)})
	if n := e.count(`SELECT count(*) FROM mcp_approvals`); n != 0 {
		t.Fatalf("invalid args were parked: %d", n)
	}
}

func TestPendingPastExpiryReadsExpired(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	if _, err := e.app.Deps.DB.Exec(`UPDATE mcp_approvals SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, aid); err != nil {
		t.Fatal(err)
	}
	if got := a.ok("check_approval", map[string]any{"approval_id": aid}); str(got, "status") != StatusExpired {
		t.Fatalf("check_approval: %v", got)
	}
	_, body := apitest.NewClient(t, e.app, "mcp.approve").Get("/v1/mcp/approvals?status=expired")
	if items, _ := body["data"].([]any); len(items) != 1 {
		t.Fatalf("expired list: %v", body)
	}
}

func TestResourceForbiddenWithoutScope(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("media.upload")
	_, err := a.cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "gorun://types"})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("resource without content.view: %v", err)
	}
}

// TestSettingsValidationParity: update_settings accepts and rejects exactly
// what the admin General / SEO screens do (same validators, same output).
func TestSettingsValidationParity(t *testing.T) {
	long := strings.Repeat("ض", 501)
	cases := []struct{ key, value string }{
		{"site.title", "  ZAD  "}, {"site.title", ""}, {"site.title", "   "}, {"site.title", long}, {"site.tagline", ""},
		{"site.locale", "ar"}, {"site.locale", "en"}, {"site.locale", "xx"},
		{"site.timezone", "Africa/Cairo"}, {"site.timezone", " UTC "}, {"site.timezone", "Local"}, {"site.timezone", ""}, {"site.timezone", "Mars/Olympus"},
		{"seo.title_template", ""}, {"seo.title_template", "{title} · ZAD"}, {"seo.title_template", "no placeholder"},
		{"seo.default_description", long}, {"seo.default_og_image", "https://example.org/og.png"}, {"seo.default_og_image", "/og.png"},
		{"seo.robots_txt", "User-agent: *\r\nDisallow:"}, {"seo.robots_txt", "x\x00"},
	}
	for _, c := range cases {
		vals, bad := validateSettings(map[string]any{c.key: c.value})
		var want string
		var wantErr error
		if strings.HasPrefix(c.key, "seo.") {
			want, wantErr = seo.ValidateSetting(c.key, c.value)
		} else {
			want, wantErr = admin.ValidateGeneralSetting(c.key, c.value)
		}
		if (wantErr != nil) != (bad[c.key] != "") || vals[c.key] != want {
			t.Errorf("%s=%q: mcp vals=%q bad=%q, admin %q %v", c.key, c.value, vals[c.key], bad[c.key], want, wantErr)
		}
	}
	if _, bad := validateSettings(map[string]any{"site.timezone": "Local"}); bad["site.timezone"] == "" {
		t.Fatal(`"Local" (the server's zone) must be rejected`)
	}
}

// I4(b): update_settings also writes site.logo (an existing media id),
// contact.email and site.posts_per_page (stored as a number).
func TestUpdateSettingsIdentityKeys(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("settings.manage", "mcp.approve_self")
	ctx := context.Background()
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	m, err := ms.Upload(ctx, "logo.png", &buf, media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	a.ok("update_settings", map[string]any{"settings": map[string]any{"site.logo": m.ID, "contact.email": "owner@example.com", "site.posts_per_page": 30}})
	var n int
	if err := e.app.Deps.Settings.Get(ctx, "site.posts_per_page", &n); err != nil || n != 30 {
		t.Fatalf("posts_per_page = %d (%v)", n, err)
	}
	if e.app.Deps.Settings.GetString(ctx, "site.logo", "") != m.ID || e.app.Deps.Settings.GetString(ctx, "contact.email", "") != "owner@example.com" {
		t.Fatal("logo/contact not stored")
	}
	for key, val := range map[string]any{"site.logo": "01MISSINGMEDIA", "contact.email": "nope", "site.posts_per_page": "0"} {
		errObj := a.fails("update_settings", map[string]any{"settings": map[string]any{key: val}})
		if fields, _ := errObj["fields"].(map[string]any); fields[key] == nil {
			t.Fatalf("%s=%v must be rejected: %v", key, val, errObj)
		}
	}
}
