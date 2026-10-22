package admin_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/db"
)

const (
	httpCFToken   = "cf-token-TESTONLY-0123456789abcdefghij"
	httpCFAccount = "0123456789abcdef0123456789abcdef"
)

// fakeSMTP is a minimal plaintext SMTP server that records one message
// and can refuse the recipient; no mail leaves the test.
type fakeSMTP struct {
	ln     net.Listener
	mu     sync.Mutex
	to     []string
	data   string
	reject bool
}

func startFakeSMTP(t *testing.T, reject bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, reject: reject}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250-fake")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH "):
			reply("235 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if f.reject {
				reply("550 mailbox unavailable")
				continue
			}
			f.mu.Lock()
			f.to = append(f.to, strings.TrimSpace(line[len("RCPT TO:"):]))
			f.mu.Unlock()
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (f *fakeSMTP) hostPort() (string, string) {
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	return host, port
}

func TestIntegrationsPageRendersBothLocalesWithoutSecrets(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	enc, err := db.Encrypt(s.app.Deps.Config.SecretKey, []byte(httpCFToken), "integrations.cloudflare.token")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"integrations.cloudflare.token": enc, "integrations.cloudflare.token_hint": "ghij", "integrations.cloudflare.account_id": httpCFAccount} {
		if err := s.app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	s.login("integrations-en@example.com", "admin", "en")
	resp, page := s.do("GET", "/admin/settings/integrations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("en page: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "Cloudflare connection", "••••ghij", "Minimum token permissions", "Not checked", "Discovered resources", "Storage and backup buckets", "Reply-To", "Send the test to", `dir="ltr"`)
	mustNotContain(t, page, httpCFToken, enc)
	s.login("integrations-ar@example.com", "admin", "ar")
	resp, page = s.do("GET", "/admin/settings/integrations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ar page: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "اتصال Cloudflare", "الموارد المكتشفة", "حاويات التخزين والنسخ الاحتياطي", `dir="rtl"`)
	mustNotContain(t, page, httpCFToken)
	// Settings tabs and navigation link to the hub.
	_, general := s.do("GET", "/admin/settings/general", nil)
	mustContain(t, general, "/admin/settings/integrations")
}

func TestIntegrationsPermissionsAndTokenSave(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	s.login("integrations-editor@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/integrations", nil); resp.StatusCode == http.StatusOK {
		t.Fatal("editor opened the integrations page")
	}
	if resp, _ := s.post("/admin/settings/integrations/cloudflare", url.Values{"token": {httpCFToken}}); resp.StatusCode == http.StatusSeeOther {
		t.Fatal("editor stored a token")
	}
	if got := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.token", ""); got != "" {
		t.Fatal("token stored without permission")
	}
	s.login("integrations-admin@example.com", "admin", "en")
	resp, page := s.post("/admin/settings/integrations/cloudflare", url.Values{"token": {"bad token"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid token: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "field-error")
	if got := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.token", ""); got != "" {
		t.Fatal("invalid token stored")
	}
	resp, page = s.post("/admin/settings/integrations/cloudflare", url.Values{"token": {httpCFToken}, "account_id": {strings.ToUpper(httpCFAccount)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d\n%s", resp.StatusCode, page)
	}
	enc := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.token", "")
	if enc == "" || strings.Contains(enc, httpCFToken) {
		t.Fatalf("token not encrypted: %q", enc)
	}
	plain, err := db.Decrypt(s.app.Deps.Config.SecretKey, enc, "integrations.cloudflare.token")
	if err != nil || string(plain) != httpCFToken {
		t.Fatalf("decrypt: %v", err)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.account_id", ""); got != httpCFAccount {
		t.Fatalf("account: %q", got)
	}
	var meta string
	if err := s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'integrations.cloudflare' ORDER BY id DESC LIMIT 1`).Scan(&meta); err != nil || strings.Contains(meta, httpCFToken) || strings.Contains(meta, enc) {
		t.Fatalf("audit leaked the token: %q %v", meta, err)
	}
	// Saving queued the background checks (accounts, scopes, 3 inventories), none ran inline.
	var queued int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind LIKE 'observe.integrations.cloudflare.%'`).Scan(&queued); err != nil || queued != 5 {
		t.Fatalf("queued jobs: %d %v", queued, err)
	}
	// Blank token on a later save keeps the stored one.
	if resp, _ = s.post("/admin/settings/integrations/cloudflare", url.Values{"token": {""}, "account_id": {httpCFAccount}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("blank save: %d", resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.token", ""); got != enc {
		t.Fatal("blank token replaced the stored token")
	}
	// Clearing removes the token and the account.
	if resp, _ = s.post("/admin/settings/integrations/cloudflare/clear", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "integrations.cloudflare.token", ""); got != "" {
		t.Fatal("clear kept the token")
	}
}

func TestIntegrationsStorageBindUsesExistingDestinations(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	s.login("integrations-storage@example.com", "admin", "en")
	// Both purposes without confirmation is refused, nothing written.
	form := url.Values{"source": {"s3"}, "endpoint": {"https://s3.eu-central-1.amazonaws.com"}, "bucket": {"shared-bucket"}, "region": {"eu-central-1"}, "name": {"Shared"},
		"access_key": {"AKIATESTONLY"}, "secret_key": {"SECRETTESTONLY"}, "purposes": {"media", "backup"}}
	resp, page := s.post("/admin/settings/integrations/storage/bind", form)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed both: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "Confirm that one bucket will serve both purposes")
	mustNotContain(t, page, "SECRETTESTONLY", "AKIATESTONLY")
	if got := s.app.Deps.Settings.GetString(ctx, "storage.active.media", ""); got != "" {
		t.Fatal("refused bind activated media")
	}
	// A Cloudflare API token is not an S3 credential: missing keys fail.
	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("access_key", "")
	bad.Set("secret_key", "")
	bad.Set("confirm_both", "1")
	if resp, page = s.post("/admin/settings/integrations/storage/bind", bad); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing keys: %d\n%s", resp.StatusCode, page)
	}
	// Private endpoints are refused (SSRF guard shared with connections).
	bad = url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("endpoint", "https://10.0.0.9")
	bad.Set("confirm_both", "1")
	if resp, page = s.post("/admin/settings/integrations/storage/bind", bad); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("private endpoint: %d\n%s", resp.StatusCode, page)
	}
	form.Set("confirm_both", "1")
	if resp, page = s.post("/admin/settings/integrations/storage/bind", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bind: %d\n%s", resp.StatusCode, page)
	}
	media := s.app.Deps.Settings.GetString(ctx, "storage.active.media", "")
	backup := s.app.Deps.Settings.GetString(ctx, "storage.active.backup", "")
	if media == "" || backup == "" || media == backup {
		t.Fatalf("destinations: media=%q backup=%q", media, backup)
	}
	profiles, err := s.app.Deps.Settings.All(ctx, "connections.profile.")
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles: %d %v", len(profiles), err)
	}
	for _, raw := range profiles {
		if strings.Contains(string(raw), "SECRETTESTONLY") {
			t.Fatal("profile stored the secret in clear")
		}
	}
	_, page = s.do("GET", "/admin/settings/integrations", nil)
	mustContain(t, page, "sites/", "/uploads/", "backups/", "shared-bucket", "Settings (database)")
	mustNotContain(t, page, "SECRETTESTONLY")
	// The existing Settings → Storage page shows the same bindings.
	_, storage := s.do("GET", "/admin/settings/storage", nil)
	mustContain(t, storage, "Shared")
	// Re-binding the same location with the same keys and a blank name reuses
	// the profile unchanged: no duplicate, no rename, no version bump.
	var before string
	for _, raw := range profiles {
		before = string(raw)
	}
	rebind := url.Values{"source": {"s3"}, "endpoint": {"https://s3.eu-central-1.amazonaws.com"}, "bucket": {"shared-bucket"}, "region": {"eu-central-1"},
		"access_key": {"AKIATESTONLY"}, "secret_key": {"SECRETTESTONLY"}, "purposes": {"media"}}
	if resp, page = s.post("/admin/settings/integrations/storage/bind", rebind); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("rebind: %d\n%s", resp.StatusCode, page)
	}
	profiles, _ = s.app.Deps.Settings.All(ctx, "connections.profile.")
	if len(profiles) != 1 {
		t.Fatalf("rebind added a profile: %d", len(profiles))
	}
	for _, raw := range profiles {
		if string(raw) != before {
			t.Fatalf("pure re-bind rewrote the profile:\n%s\n%s", before, raw)
		}
	}
	// Unknown inventory kinds are 404, not a misleading flash.
	if resp, _ = s.post("/admin/settings/integrations/cloudflare/inventory/queues/refresh", url.Values{}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind: %d", resp.StatusCode)
	}
	// Probe needs the capability-test permission and only queues work.
	resp, _ = s.post("/admin/settings/integrations/storage/backup/probe", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("probe: %d", resp.StatusCode)
	}
	var queued int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind = 'observe.operation.s3'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("probe jobs: %d %v", queued, err)
	}
	// The read-only check was queued by the bind itself; nothing ran inline.
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind = 'observe.connection'`).Scan(&queued); err != nil || queued == 0 {
		t.Fatalf("connectivity jobs: %d %v", queued, err)
	}
	s.login("integrations-storage-editor@example.com", "editor", "en")
	if resp, _ = s.post("/admin/settings/integrations/storage/backup/probe", url.Values{}); resp.StatusCode == http.StatusSeeOther {
		t.Fatal("editor queued a probe")
	}
}

func TestIntegrationsMailTestReportsAcceptanceNotDelivery(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t, false)
	host, port := smtp.hostPort()
	u := s.login("integrations-mail@example.com", "admin", "en")
	_, page := s.do("GET", "/admin/settings/integrations", nil)
	token := extractInput(page, "edit_token")
	form := url.Values{"edit_token": {token}, "provider": {"smtp"}, "host": {host}, "port": {port}, "tls": {"none"}, "from": {"noreply@example.com"}, "from_name": {"Demo"}, "reply_to": {"support@example.com"}, "username": {"user"}, "password": {"SMTPSECRETTESTONLY"}}
	resp, page := s.post("/admin/settings/integrations/mail", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save mail: %d\n%s", resp.StatusCode, page)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "mail.reply_to", ""); got != "support@example.com" {
		t.Fatalf("reply_to: %q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "mail.password", ""); got == "" || strings.Contains(got, "SMTPSECRETTESTONLY") {
		t.Fatalf("password not encrypted: %q", got)
	}
	_, page = s.do("GET", "/admin/settings/integrations", nil)
	mustContain(t, page, host+":"+port, "support@example.com", "A password is stored")
	mustNotContain(t, page, "SMTPSECRETTESTONLY")
	// Stale edit token → 409 and no change.
	form.Set("edit_token", "stale")
	form.Set("reply_to", "other@example.com")
	if resp, _ = s.post("/admin/settings/integrations/mail", form); resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale token: %d", resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "mail.reply_to", ""); got != "support@example.com" {
		t.Fatal("stale save changed settings")
	}
	// Test to a chosen address: provider acceptance reported, delivery not verified.
	resp, page = s.post("/admin/settings/integrations/mail/test", url.Values{"to": {"qa-inbox@example.com"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "The provider accepted the message", "Not verified", "qa***@example.com")
	smtp.mu.Lock()
	to, data := smtp.to, smtp.data
	smtp.mu.Unlock()
	if len(to) != 1 || !strings.Contains(to[0], "qa-inbox@example.com") || !strings.Contains(data, "Reply-To: <support@example.com>") {
		t.Fatalf("smtp saw to=%v data=%q", to, data)
	}
	if resp, page = s.post("/admin/settings/integrations/mail/test", url.Values{"to": {"not an address"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad recipient: %d\n%s", resp.StatusCode, page)
	}
	// The admin audit log keeps the full recipient of a third-party test send.
	var meta string
	if err := s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'mail.test_sent' ORDER BY id DESC LIMIT 1`).Scan(&meta); err != nil || !strings.Contains(meta, "qa-inbox@example.com") || !strings.Contains(meta, `"accepted":true`) || !strings.Contains(meta, `"own_address":false`) {
		t.Fatalf("audit: %q %v", meta, err)
	}
	// Evidence is recorded for the requesting operator.
	var result string
	if err := s.app.Deps.DB.QueryRow(`SELECT result_json FROM observations WHERE kind = 'operation.mail' AND key = ?`, "user:"+u.ID).Scan(&result); err != nil || !strings.Contains(result, "chosen_address") {
		t.Fatalf("evidence: %q %v", result, err)
	}
	// A rejected recipient is reported as not accepted, with a sanitized reason.
	rejecting := startFakeSMTP(t, true)
	host, port = rejecting.hostPort()
	_, page = s.do("GET", "/admin/settings/integrations", nil)
	form.Set("edit_token", extractInput(page, "edit_token"))
	form.Set("host", host)
	form.Set("port", port)
	form.Set("password", "")
	if resp, page = s.post("/admin/settings/integrations/mail", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch smtp: %d\n%s", resp.StatusCode, page)
	}
	resp, page = s.post("/admin/settings/integrations/mail/test", url.Values{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rejected test: %d", resp.StatusCode)
	}
	mustContain(t, page, "did not accept the message")
	// At most five test sends per operator per window; the sixth is refused.
	sent := 0
	for i := 0; i < 6; i++ {
		resp, _ = s.post("/admin/settings/integrations/mail/test", url.Values{})
		if resp.StatusCode == http.StatusOK {
			sent++
		} else if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("send %d: %d", i, resp.StatusCode)
		}
	}
	if sent != 3 { // two sends above plus three more fit in the burst of five
		t.Fatalf("rate limit let %d extra sends through", sent)
	}
	// Without the capability-test permission the test route is forbidden.
	s.login("integrations-viewer@example.com", "editor", "en")
	if resp, _ = s.post("/admin/settings/integrations/mail/test", url.Values{}); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Fatalf("editor sent a test mail: %d", resp.StatusCode)
	}
}

func extractInput(page, name string) string {
	marker := `name="` + name + `" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		return ""
	}
	rest := page[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func TestIntegrationsAPIContractsAndScopes(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	ctx := context.Background()
	settingsClient := apitest.NewClient(t, app, "settings.manage")
	limited := apitest.NewClient(t, app, "content.view")
	if status, _ := limited.Get("/site/integrations"); status != http.StatusForbidden {
		t.Fatalf("limited key got %d", status)
	}
	if status, _ := settingsClient.Anonymous().Get("/site/integrations"); status != http.StatusUnauthorized {
		t.Fatalf("anonymous got %d", status)
	}
	status, body := settingsClient.Get("/site/integrations")
	if status != http.StatusOK {
		t.Fatalf("report: %d %+v", status, body)
	}
	data := apitest.Data(body)
	cf := data["cloudflare"].(map[string]any)
	if cf["configured"] != false || len(cf["minimum_scopes"].([]any)) != 5 {
		t.Fatalf("unconfigured view: %+v", cf)
	}
	for _, s := range cf["scopes"].([]any) {
		if s.(map[string]any)["status"] != "not_checked" {
			t.Fatalf("scope before any check: %+v", s)
		}
	}
	if status, body := settingsClient.Post("/site/integrations/cloudflare/check", nil); status != http.StatusConflict || apitest.ErrorCode(body) != "cloudflare_not_configured" {
		t.Fatalf("check without token: %d %+v", status, body)
	}
	if status, body := settingsClient.Post("/site/integrations/cloudflare/inventory/r2/refresh", nil); status != http.StatusConflict || apitest.ErrorCode(body) != "cloudflare_not_configured" {
		t.Fatalf("refresh without token: %d %+v", status, body)
	}
	if status, body := settingsClient.Post("/site/integrations/cloudflare/inventory/queues/refresh", nil); status != http.StatusNotFound {
		t.Fatalf("unknown kind: %d %+v", status, body)
	}
	status, body = settingsClient.Do("PUT", "/site/integrations/cloudflare", map[string]any{"token": "bad token"}, nil).Code, nil
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid token: %d", status)
	}
	w := settingsClient.Do("PUT", "/site/integrations/cloudflare", map[string]any{"token": httpCFToken}, nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), httpCFToken) {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var saved struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Data["token_hint"] != "ghij" || saved.Data["configured"] != true {
		t.Fatalf("saved view: %+v", saved.Data)
	}
	if status, body := settingsClient.Post("/site/integrations/cloudflare/inventory/r2/refresh", nil); status != http.StatusConflict || apitest.ErrorCode(body) != "account_required" {
		t.Fatalf("refresh without account: %d %+v", status, body)
	}
	w = settingsClient.Do("PUT", "/site/integrations/cloudflare", map[string]any{"account_id": httpCFAccount}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("account: %d %s", w.Code, w.Body.String())
	}
	w = settingsClient.Do("POST", "/site/integrations/cloudflare/check", nil, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("check: %d %s", w.Code, w.Body.String())
	}
	w = settingsClient.Do("POST", "/site/integrations/cloudflare/inventory/r2/refresh", nil, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
	}
	// Concurrent refreshes of the same key share one job.
	var dedup struct {
		Data map[string]any `json:"data"`
	}
	w = settingsClient.Do("POST", "/site/integrations/cloudflare/inventory/r2/refresh", nil, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &dedup)
	if w.Code != http.StatusAccepted || dedup.Data["deduplicated"] != true {
		t.Fatalf("dedup: %d %+v", w.Code, dedup.Data)
	}
	var jobs int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind = 'observe.integrations.cloudflare.inventory' AND payload_json LIKE '%"key":"r2"%'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("inventory jobs: %d %v", jobs, err)
	}
	// A refresh of the manual identity kind is refused by the generic observations API.
	if status, body := settingsClient.Post("/system/observations/integrations.cloudflare.edge_identity/edge-worker/refresh", nil); status != http.StatusConflict || apitest.ErrorCode(body) != "manual_only" {
		t.Fatalf("identity refresh: %d %+v", status, body)
	}
	// Worker identity without a discovered account/worker name is a validation error, never a network call.
	status, body = settingsClient.Post("/site/integrations/cloudflare/workers/bad%20name/identify", map[string]any{"origin": "https://edge.example", "manager_key": strings.Repeat("a", 43)})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad worker: %d %+v", status, body)
	}
	status, body = settingsClient.Post("/site/integrations/cloudflare/workers/edge-worker/identify", map[string]any{"origin": "https://127.0.0.1", "manager_key": strings.Repeat("a", 43)})
	if status != http.StatusUnprocessableEntity || apitest.ErrorCode(body) != "validation_failed" {
		t.Fatalf("private origin: %d %+v", status, body)
	}
	// Storage bind and mail test scopes.
	status, body = settingsClient.Post("/site/integrations/storage/bind", map[string]any{"source": "s3", "endpoint": "https://10.1.1.1", "bucket": "b", "access_key": "a", "secret_key": "s", "purposes": []string{"media"}})
	if status != http.StatusUnprocessableEntity || apitest.ErrorCode(body) != "validation_failed" {
		t.Fatalf("private bind: %d %+v", status, body)
	}
	if status, body := settingsClient.Post("/site/integrations/mail/test", map[string]any{}); status != http.StatusForbidden {
		t.Fatalf("mail test without capability scope: %d %+v", status, body)
	}
	tester := apitest.NewClient(t, app, "system.capability_test")
	if status, body := tester.Post("/site/integrations/mail/test", map[string]any{}); status != http.StatusConflict || apitest.ErrorCode(body) != "capability_disabled" {
		t.Fatalf("mail test unconfigured: %d %+v", status, body)
	}
	// Settings API carries mail.reply_to.
	if status, body := settingsClient.Patch("/settings", map[string]any{"mail.reply_to": "support@example.com"}); status != http.StatusOK {
		t.Fatalf("patch reply_to: %d %+v", status, body)
	}
	if got := app.Deps.Settings.GetString(ctx, "mail.reply_to", ""); got != "support@example.com" {
		t.Fatalf("reply_to: %q", got)
	}
	// OpenAPI documents every operation with its scopes.
	status, spec := settingsClient.Anonymous().Get("/api/v1/openapi.json")
	if status != http.StatusOK {
		t.Fatal(status)
	}
	paths := spec["paths"].(map[string]any)
	for path, scope := range map[string]string{
		"/v1/site/integrations": "settings.manage", "/v1/site/integrations/cloudflare": "settings.manage", "/v1/site/integrations/cloudflare/check": "settings.manage",
		"/v1/site/integrations/cloudflare/inventory/{kind}": "settings.manage", "/v1/site/integrations/cloudflare/inventory/{kind}/refresh": "settings.manage",
		"/v1/site/integrations/cloudflare/workers/{name}/identify": "settings.manage", "/v1/site/integrations/storage/bind": "settings.manage", "/v1/site/integrations/mail/test": "system.capability_test",
	} {
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("%s missing from OpenAPI", path)
		}
		raw, _ := json.Marshal(item)
		if !strings.Contains(string(raw), scope) {
			t.Fatalf("%s does not document scope %s: %s", path, scope, raw)
		}
	}
}
