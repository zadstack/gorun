package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/metrics"
)

const (
	testBotToken    = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	testBotUsername = "gorun_test_bot"
)

// bootNotify boots admin and content without job workers, with a fake mailer.
func bootNotify(t *testing.T) (*site, *admin.NotifyHarness, *admin.FakeMail) {
	t.Helper()
	s := bootSite(t, nil, kernel.BootOptions{StartJobs: false}, content.New())
	n := admin.Notify(s.app)
	mail := &admin.FakeMail{}
	n.UseMail(mail)
	return s, n, mail
}

// setPrefs writes a user's preferences directly; channels start an hour ago.
func (s *site) setPrefs(userID, emailMode string, digestHour int, extra ...string) {
	s.t.Helper()
	since := db.FormatTime(time.Now().Add(-time.Hour))
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO notification_prefs (user_id, email_mode, digest_hour, email_since, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (user_id) DO UPDATE SET email_mode = excluded.email_mode, digest_hour = excluded.digest_hour,
		email_since = excluded.email_since`, userID, emailMode, digestHour, since, since); err != nil {
		s.t.Fatal(err)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if _, err := s.app.Deps.DB.Exec(`UPDATE notification_prefs SET `+extra[i]+` = ? WHERE user_id = ?`, extra[i+1], userID); err != nil {
			s.t.Fatal(err)
		}
	}
}

// pending inserts content submitted for review by author.
func (s *site) pending(id, title, authorID string) {
	s.t.Helper()
	now := db.Now()
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO content (id, type, slug, title, author_id, status, created_at, updated_at)
		VALUES (?, 'post', ?, ?, ?, 'pending', ?, ?)`, id, id, title, authorID, now, now); err != nil {
		s.t.Fatal(err)
	}
}

func (s *site) deliveryStatus(userID, channel, key string) string {
	s.t.Helper()
	var st string
	_ = s.app.Deps.DB.QueryRow(`SELECT status FROM notification_deliveries WHERE user_id = ? AND channel = ? AND key = ?`, userID, channel, key).Scan(&st)
	return st
}

func TestNotifyEmailDedupeAcrossRuns(t *testing.T) {
	s, n, mail := bootNotify(t)
	ctx := context.Background()
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	s.setPrefs(editor.ID, "immediate", 7)
	s.pending("c1", "Review me", author.ID)

	before := metrics.NotificationCount(metrics.ChannelEmail, true)
	if _, sent, _, err := n.Run(ctx); err != nil || sent != 1 {
		t.Fatalf("first run: sent=%d err=%v", sent, err)
	}
	msgs := mail.Messages()
	if len(msgs) != 1 || msgs[0].To != editor.Email || !strings.Contains(msgs[0].Text, "Review me") ||
		!strings.Contains(msgs[0].Text, "/admin/notifications/go?key=content.review%3Ac1") {
		t.Fatalf("first email: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Text, "/notifications/unsubscribe?token=") || strings.Contains(msgs[0].HTML, "<script") {
		t.Fatalf("email links: %s", msgs[0].Text)
	}
	if got := metrics.NotificationCount(metrics.ChannelEmail, true); got != before+1 {
		t.Fatalf("metric sent: %d → %d", before, got)
	}
	if st := s.deliveryStatus(editor.ID, "email", "content.review:c1"); st != "sent" {
		t.Fatalf("delivery status %q", st)
	}

	// A second run (as after a restart) finds nothing new.
	if _, sent, _, err := n.Run(ctx); err != nil || sent != 0 || len(mail.Messages()) != 1 {
		t.Fatalf("second run: sent=%d msgs=%d err=%v", sent, len(mail.Messages()), err)
	}

	// A new item is sent alone.
	s.pending("c2", "Second draft", author.ID)
	if _, _, _, err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	msgs = mail.Messages()
	if len(msgs) != 2 || !strings.Contains(msgs[1].Text, "Second draft") || strings.Contains(msgs[1].Text, "Review me") {
		t.Fatalf("third run emails: %+v", msgs)
	}
	// The author (no review permission on own content) never got anything.
	for _, m := range msgs {
		if m.To == author.Email {
			t.Fatalf("author emailed: %+v", m)
		}
	}
}

func TestNotifyEmailFailureRetriesThenStops(t *testing.T) {
	s, n, mail := bootNotify(t)
	ctx := context.Background()
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	s.setPrefs(editor.ID, "immediate", 7)
	s.pending("c1", "Review me", author.ID)
	mail.Fail = io.ErrUnexpectedEOF
	for i := 0; i < 4; i++ {
		if _, _, _, err := n.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var attempts int
	_ = s.app.Deps.DB.QueryRow(`SELECT attempts FROM notification_deliveries WHERE user_id = ? AND key = 'content.review:c1'`, editor.ID).Scan(&attempts)
	if attempts != 3 || s.deliveryStatus(editor.ID, "email", "content.review:c1") != "failed" {
		t.Fatalf("attempts=%d status=%q", attempts, s.deliveryStatus(editor.ID, "email", "content.review:c1"))
	}
}

func TestNotifyPermissionRecheckedAtSendTime(t *testing.T) {
	s, n, mail := bootNotify(t)
	ctx := context.Background()
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	s.setPrefs(editor.ID, "immediate", 7)

	// The editor loses content.publish before the item appears.
	role := "author"
	if _, err := s.users().Update(ctx, editor.ID, auth.UserPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	s.pending("c1", "Review me", author.ID)
	if _, sent, _, err := n.Run(ctx); err != nil || sent != 0 || len(mail.Messages()) != 0 {
		t.Fatalf("sent=%d msgs=%v err=%v", sent, mail.Messages(), err)
	}

	// A disabled user receives nothing either, even with the permission back.
	role = "editor"
	status := auth.StatusDisabled
	if _, err := s.users().Update(ctx, editor.ID, auth.UserPatch{Role: &role, Status: &status}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := n.Run(ctx); err != nil || len(mail.Messages()) != 0 {
		t.Fatalf("disabled user emailed: %v %v", mail.Messages(), err)
	}
}

func TestNotifyMutedCategoryNotSent(t *testing.T) {
	s, n, mail := bootNotify(t)
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	s.setPrefs(editor.ID, "immediate", 7, "muted", `["review"]`)
	s.pending("c1", "Review me", author.ID)
	if _, _, _, err := n.Run(context.Background()); err != nil || len(mail.Messages()) != 0 {
		t.Fatalf("muted category emailed: %v %v", mail.Messages(), err)
	}
}

func TestNotifyDigestByHour(t *testing.T) {
	s, n, mail := bootNotify(t)
	ctx := context.Background()
	author := s.createUser("writer@example.com", "author", "ar")
	editor := s.createUser("editor@example.com", "editor", "ar")
	now := time.Now().UTC()
	s.setPrefs(editor.ID, "digest", now.Hour())
	s.pending("c1", "مسودة", author.ID)
	s.pending("c2", "Second", author.ID)

	n.SetNow(now.Add(-time.Hour))
	if _, _, _, err := n.Run(ctx); err != nil || len(mail.Messages()) != 0 {
		t.Fatalf("digest before its hour: %v %v", mail.Messages(), err)
	}
	n.SetNow(now)
	if _, _, _, err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := mail.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "الملخص اليومي") || !strings.Contains(msgs[0].HTML, `dir="rtl"`) ||
		!strings.Contains(msgs[0].Text, "مسودة") || !strings.Contains(msgs[0].Text, "Second") {
		t.Fatalf("digest email: %+v", msgs)
	}
	// Once a day: a later run in the same hour sends nothing.
	n.SetNow(now.Add(time.Minute))
	s.pending("c3", "Third", author.ID)
	if _, _, _, err := n.Run(ctx); err != nil || len(mail.Messages()) != 1 {
		t.Fatalf("second digest the same day: %d %v", len(mail.Messages()), err)
	}
	// The next day at the same hour: only what was not sent yet.
	n.SetNow(now.Add(24 * time.Hour))
	if _, _, _, err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	msgs = mail.Messages()
	if len(msgs) != 2 || !strings.Contains(msgs[1].Text, "Third") || strings.Contains(msgs[1].Text, "Second") {
		t.Fatalf("next-day digest: %+v", msgs)
	}
}

func TestNotifyUnsubscribeToken(t *testing.T) {
	s, n, _ := bootNotify(t)
	a := s.createUser("a@example.com", "editor", "en")
	b := s.createUser("b@example.com", "editor", "en")
	s.setPrefs(a.ID, "immediate", 7)
	s.setPrefs(b.ID, "immediate", 7)
	token := n.UnsubscribeToken(a.ID)
	s.c = newClient()

	// GET asks and changes nothing (link scanners).
	resp, body := s.do("GET", "/notifications/unsubscribe?token="+url.QueryEscape(token), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "data-unsubscribe") || strings.Contains(body, "a@example.com") {
		t.Fatalf("GET: %d %s", resp.StatusCode, body)
	}
	if mode := s.emailMode(a.ID); mode != "immediate" {
		t.Fatalf("GET changed mode to %q", mode)
	}
	// Tampered tokens are refused, including one naming another user.
	forged := strings.Replace(token, strings.Split(token, ".")[1], strings.Split(n.UnsubscribeToken(b.ID), ".")[1], 1)
	for _, bad := range []string{"", "v1.x.y", token + "x", forged} {
		resp, _ := s.do("POST", "/notifications/unsubscribe", url.Values{"token": {bad}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("token %q: %d", bad, resp.StatusCode)
		}
	}
	if s.emailMode(b.ID) != "immediate" {
		t.Fatal("forged token changed another user")
	}
	resp, body = s.do("POST", "/notifications/unsubscribe", url.Values{"token": {token}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "data-unsubscribe-done") {
		t.Fatalf("POST: %d %s", resp.StatusCode, body)
	}
	if s.emailMode(a.ID) != "off" || s.emailMode(b.ID) != "immediate" {
		t.Fatalf("modes after unsubscribe: %q %q", s.emailMode(a.ID), s.emailMode(b.ID))
	}
	if s.auditCount("notifications.email_unsubscribed", a.ID) != 1 {
		t.Fatal("unsubscribe not audited")
	}
}

func (s *site) emailMode(userID string) string {
	var m string
	_ = s.app.Deps.DB.QueryRow(`SELECT email_mode FROM notification_prefs WHERE user_id = ?`, userID).Scan(&m)
	return m
}

func TestNotifyBotTokenNeverRendered(t *testing.T) {
	s, _, _ := bootNotify(t)
	s.login("root@example.com", "admin", "en")
	resp, body := s.post("/admin/settings/notifications", url.Values{"bot_token": {"not-a-token-" + testBotToken}, "bot_username": {testBotUsername}})
	if resp.StatusCode != http.StatusUnprocessableEntity || strings.Contains(body, testBotToken) {
		t.Fatalf("invalid token echoed or accepted: %d", resp.StatusCode)
	}
	resp, _ = s.post("/admin/settings/notifications", url.Values{"bot_token": {testBotToken}, "bot_username": {"@" + testBotUsername}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	_, body = s.do("GET", "/admin/settings/notifications", nil)
	if strings.Contains(body, testBotToken) || strings.Contains(body, strings.Split(testBotToken, ":")[1]) || !strings.Contains(body, "data-bot-token-set") {
		t.Fatalf("settings page leaks or misses state:\n%s", body)
	}
	var stored string
	_ = s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key = 'notify.telegram.bot_token'`).Scan(&stored)
	if stored == "" || strings.Contains(stored, strings.Split(testBotToken, ":")[1]) {
		t.Fatalf("token stored in clear: %q", stored)
	}
	if s.auditCount("notifications.telegram_bot_updated") != 1 {
		t.Fatal("bot change not audited")
	}
	var meta string
	_ = s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'notifications.telegram_bot_updated'`).Scan(&meta)
	if strings.Contains(meta, strings.Split(testBotToken, ":")[1]) {
		t.Fatalf("audit leaks token: %s", meta)
	}
	// Non-administrators cannot reach the page.
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/notifications", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}

// configureBot saves the bot through the settings page as an administrator.
func (s *site) configureBot() {
	s.t.Helper()
	s.login("root@example.com", "admin", "en")
	if resp, _ := s.post("/admin/settings/notifications", url.Values{"bot_token": {testBotToken}, "bot_username": {testBotUsername}}); resp.StatusCode != http.StatusSeeOther {
		s.t.Fatalf("configure bot: %d", resp.StatusCode)
	}
}

func (s *site) webhook(path, header, text string) (*http.Response, string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"update_id": 1, "message": map[string]any{"text": text,
		"chat": map[string]any{"id": 4242, "type": "private"}, "from": map[string]any{"language_code": "en"}}})
	req, _ := http.NewRequest("POST", s.srv.URL+"/webhooks/telegram/"+path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", header)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestTelegramWebhookAuthAndLinkCodes(t *testing.T) {
	s, n, _ := bootNotify(t)
	ctx := context.Background()
	user := s.createUser("ed@example.com", "editor", "en")

	// No bot configured: the endpoint does not exist.
	if resp, _ := s.webhook("0123456789abcdef0123456789abcdef01234567", "x", "/start x"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured: %d", resp.StatusCode)
	}
	s.configureBot()
	path, header := n.Webhook(ctx)
	if path == "" || header == "" {
		t.Fatal("no webhook secret after saving the bot")
	}
	code, err := n.NewLinkCode(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := s.webhook(strings.Repeat("0", len(path)), header, "/start "+code); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong path: %d", resp.StatusCode)
	}
	if resp, _ := s.webhook(path, "", "/start "+code); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing header: %d", resp.StatusCode)
	}
	// Change the last character to a different one: the header is random, so a
	// fixed replacement would equal the real value one time in sixteen.
	last := "0"
	if strings.HasSuffix(header, "0") {
		last = "1"
	}
	if resp, _ := s.webhook(path, header[:len(header)-1]+last, "/start "+code); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong header: %d", resp.StatusCode)
	}
	var stored int
	_ = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM notification_telegram_codes WHERE code_hash = ?`, code).Scan(&stored)
	if stored != 0 {
		t.Fatal("link code stored in clear")
	}

	resp, body := s.webhook(path, header, "/start "+code)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"method":"sendMessage"`) || !strings.Contains(body, "Linked.") {
		t.Fatalf("link: %d %s", resp.StatusCode, body)
	}
	var chat string
	var enabled bool
	_ = s.app.Deps.DB.QueryRow(`SELECT telegram_chat_id, telegram_enabled FROM notification_prefs WHERE user_id = ?`, user.ID).Scan(&chat, &enabled)
	if chat != "4242" || !enabled {
		t.Fatalf("chat=%q enabled=%v", chat, enabled)
	}
	if s.auditCount("notifications.telegram_linked", user.ID) != 1 {
		t.Fatal("link not audited")
	}
	// Single use.
	if _, body := s.webhook(path, header, "/start "+code); !strings.Contains(body, "not valid") {
		t.Fatalf("code reused: %s", body)
	}
	// Expired after 15 minutes.
	code2, _ := n.NewLinkCode(ctx, user.ID)
	n.SetNow(time.Now().Add(16 * time.Minute))
	if _, body := s.webhook(path, header, "/start "+code2); !strings.Contains(body, "not valid") {
		t.Fatalf("expired code accepted: %s", body)
	}
	n.SetNow(time.Now())

	// Unlink from My account.
	s.signIn(user.Email)
	if resp, _ := s.post("/admin/account/notifications/telegram/unlink", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlink: %d", resp.StatusCode)
	}
	_ = s.app.Deps.DB.QueryRow(`SELECT telegram_chat_id, telegram_enabled FROM notification_prefs WHERE user_id = ?`, user.ID).Scan(&chat, &enabled)
	if chat != "" || enabled || s.auditCount("notifications.telegram_unlinked", user.ID) != 1 {
		t.Fatalf("after unlink chat=%q enabled=%v", chat, enabled)
	}
}

// fakeTelegram is a local Bot API: status codes are taken from codes in
// order (the last repeats) and every request is recorded.
type fakeTelegram struct {
	mu    sync.Mutex
	codes []int
	reqs  []string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	code := http.StatusOK
	if len(f.codes) > 0 {
		code = f.codes[0]
		if len(f.codes) > 1 {
			f.codes = f.codes[1:]
		}
	}
	b, _ := io.ReadAll(r.Body)
	f.reqs = append(f.reqs, r.URL.Path+" "+string(b))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if code == http.StatusOK {
		io.WriteString(w, `{"ok":true,"result":{}}`)
		return
	}
	io.WriteString(w, `{"ok":false,"description":"Forbidden: bot was blocked by the user","parameters":{"retry_after":1}}`)
}

func (f *fakeTelegram) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func linkUser(t *testing.T, s *site, n *admin.NotifyHarness, userID string) {
	t.Helper()
	ctx := context.Background()
	path, header := n.Webhook(ctx)
	code, err := n.NewLinkCode(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, body := s.webhook(path, header, "/start "+code); !strings.Contains(body, "Linked.") {
		t.Fatalf("link: %s", body)
	}
	// The channel starts at link time; let earlier items count in the test.
	if _, err := s.app.Deps.DB.Exec(`UPDATE notification_prefs SET telegram_since = ? WHERE user_id = ?`, db.FormatTime(time.Now().Add(-time.Hour)), userID); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramSendRetriesAndDisablesAfter403(t *testing.T) {
	s, n, _ := bootNotify(t)
	ctx := context.Background()
	author := s.createUser("writer@example.com", "author", "en")
	editor := s.createUser("editor@example.com", "editor", "en")
	s.configureBot()
	linkUser(t, s, n, editor.ID)

	fake := &fakeTelegram{codes: []int{http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusOK}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	var waits []time.Duration
	n.UseTelegram(srv.URL, &waits)

	s.pending("c1", "Review me", author.ID)
	before := metrics.NotificationCount(metrics.ChannelTelegram, true)
	if _, sent, _, err := n.Run(ctx); err != nil || sent != 1 {
		t.Fatalf("retry run: sent=%d err=%v", sent, err)
	}
	reqs := fake.requests()
	if len(reqs) != 3 || !strings.HasPrefix(reqs[2], "/bot"+testBotToken+"/sendMessage") || !strings.Contains(reqs[2], `"chat_id":"4242"`) ||
		!strings.Contains(reqs[2], "Review me") {
		t.Fatalf("requests: %v", reqs)
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != time.Second {
		t.Fatalf("backoff waits: %v", waits)
	}
	if metrics.NotificationCount(metrics.ChannelTelegram, true) != before+1 {
		t.Fatal("telegram sent metric")
	}

	// The bot gets blocked: three 403 runs turn Telegram off for the user.
	fake.mu.Lock()
	fake.codes = []int{http.StatusForbidden}
	fake.mu.Unlock()
	for i := 2; i <= 4; i++ {
		s.pending("x"+string(rune('0'+i)), "Item", author.ID)
		if _, _, _, err := n.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var enabled bool
	var reason string
	_ = s.app.Deps.DB.QueryRow(`SELECT telegram_enabled, telegram_disabled FROM notification_prefs WHERE user_id = ?`, editor.ID).Scan(&enabled, &reason)
	if enabled || reason != "blocked" || s.auditCount("notifications.telegram_disabled", editor.ID) != 1 {
		t.Fatalf("after 403s enabled=%v reason=%q", enabled, reason)
	}
	// No retries on 403, and nothing more once disabled.
	count := len(fake.requests())
	s.pending("x9", "Later", author.ID)
	if _, _, _, err := n.Run(ctx); err != nil || len(fake.requests()) != count {
		t.Fatalf("sent after disable: %d → %d", count, len(fake.requests()))
	}
	if count != 6 {
		t.Fatalf("403 retried: %d requests", count)
	}
	// The page tells the user.
	s.signIn(editor.Email)
	_, body := s.do("GET", "/admin/account/notifications", nil)
	mustContain(t, body, "data-telegram-blocked")
}

func TestNotifyPreferencesPage(t *testing.T) {
	s, n, mail := bootNotify(t)
	s.configureBot()
	ed := s.login("ed@example.com", "editor", "ar")

	_, body := s.do("GET", "/admin/account/notifications", nil)
	mustContain(t, body, `data-notify-prefs`, `name="email_mode"`, `data-notify-categories`, `name="deliver_review"`, `data-telegram-link-start`, `dir="rtl"`)
	if strings.Contains(body, `name="deliver_failures"`) {
		t.Fatal("editor offered a category they cannot see")
	}
	resp, _ := s.post("/admin/account/notifications", url.Values{"email_mode": {"digest"}, "digest_hour": {"9"}, "deliver_review": {"on"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	var mode, muted, since string
	var hour int
	_ = s.app.Deps.DB.QueryRow(`SELECT email_mode, digest_hour, muted, email_since FROM notification_prefs WHERE user_id = ?`, ed.ID).Scan(&mode, &hour, &muted, &since)
	if mode != "digest" || hour != 9 || muted != `["comments"]` || since == "" {
		t.Fatalf("saved prefs: %q %d %q %q", mode, hour, muted, since)
	}
	if resp, _ := s.post("/admin/account/notifications", url.Values{"email_mode": {"loud"}, "digest_hour": {"25"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save: %d", resp.StatusCode)
	}

	// Link flow: the page shows the code once and a t.me deep link.
	_, body = s.post("/admin/account/notifications/telegram/link", nil)
	mustContain(t, body, "data-telegram-code", "https://t.me/"+testBotUsername+"?start=")

	// Test sends are rate limited per channel.
	for i := 0; i < 3; i++ {
		s.post("/admin/account/notifications/test/email", nil)
	}
	if len(mail.Messages()) != 3 || !strings.Contains(mail.Messages()[0].Subject, "إشعار تجريبي") {
		t.Fatalf("test emails: %+v", mail.Messages())
	}
	s.post("/admin/account/notifications/test/email", nil)
	if len(mail.Messages()) != 3 {
		t.Fatal("test send not rate limited")
	}
	_ = n
}

func TestNotifyMailNotConfigured(t *testing.T) {
	s, _, mail := bootNotify(t)
	mail.Unconfigured = true
	s.login("ed@example.com", "editor", "en")
	_, body := s.do("GET", "/admin/account/notifications", nil)
	mustContain(t, body, "data-mail-not-configured", "Configure mail first")
	resp, body := s.post("/admin/account/notifications", url.Values{"email_mode": {"immediate"}, "digest_hour": {"7"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Configure mail first") {
		t.Fatalf("enable without mail: %d", resp.StatusCode)
	}
	resp, _ = s.post("/admin/account/notifications/test/email", nil)
	if resp.StatusCode != http.StatusSeeOther || len(mail.Messages()) != 0 {
		t.Fatalf("test without mail: %d %d", resp.StatusCode, len(mail.Messages()))
	}
}

func TestTelegramRegisterWebhook(t *testing.T) {
	fake := &fakeTelegram{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	// An http base URL is refused before anything is sent.
	s, n, _ := bootNotify(t)
	n.UseTelegram(srv.URL, nil)
	s.configureBot()
	s.post("/admin/settings/notifications/webhook", nil)
	if len(fake.requests()) != 0 {
		t.Fatalf("setWebhook over http base URL: %v", fake.requests())
	}

	s = bootSite(t, func(c *config.Config) { c.BaseURL = "https://cms.example.test" }, kernel.BootOptions{StartJobs: false}, content.New())
	n = admin.Notify(s.app)
	n.UseTelegram(srv.URL, nil)
	s.configureBot()
	if resp, _ := s.post("/admin/settings/notifications/webhook", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	path, header := n.Webhook(context.Background())
	reqs := fake.requests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0], "/bot"+testBotToken+"/setWebhook ") ||
		!strings.Contains(reqs[0], `"url":"https://cms.example.test/webhooks/telegram/`+path+`"`) || !strings.Contains(reqs[0], `"secret_token":"`+header+`"`) {
		t.Fatalf("setWebhook request: %v", reqs)
	}
	if s.auditCount("notifications.telegram_webhook_registered") != 1 {
		t.Fatal("webhook registration not audited")
	}
	_, body := s.do("GET", "/admin/settings/notifications", nil)
	mustContain(t, body, "data-webhook-ready")
	if strings.Contains(body, path) || strings.Contains(body, header) {
		t.Fatal("settings page renders the webhook secret")
	}
	// A new token rotates the secret: the old webhook path stops working.
	s.post("/admin/settings/notifications", url.Values{"bot_token": {"987654321:BBHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"}, "bot_username": {testBotUsername}})
	if resp, _ := s.webhook(path, header, "/start x"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("old webhook path after rotation: %d", resp.StatusCode)
	}
}
