package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/metrics"
)

// Outbox for the derived notifications (docs/NOTIFICATIONS.md). Nothing is
// queued when an item appears: every deliverEvery a job recomputes each
// opted-in user's items as that user and sends the keys not yet recorded in
// notification_deliveries for the channel.
const (
	deliverEvery       = 5 * time.Minute
	deliverJob         = "notifications.deliver"
	deliverMaxUsers    = 200
	deliverMaxItems    = 20
	deliverMaxAttempts = 3
	deliverKeepFor     = 14 * 24 * time.Hour
	detailMaxRunes     = 80
	testSendBurst      = 3
	testSendWindow     = 10 * time.Minute
	notifyPrefsPage    = "/admin/account/notifications"
	notifyServiceName  = "admin.notify-delivery"
)

// notifySender is the part of the mailer delivery uses (a fake in tests).
type notifySender interface {
	Configured(ctx context.Context) bool
	SendRequired(ctx context.Context, to, subject, html, text string) error
}

// notifyDelivery sends notifications by email and Telegram.
type notifyDelivery struct {
	d     *kernel.Deps
	users auth.Service
	mail  notifySender
	tpl   *mailer.Mailer // renders the email templates
	pages *httpx.Templates
	tg    *telegramClient
	now   func() time.Time

	testMu sync.Mutex
	tests  map[string][]time.Time // user|channel → recent test sends
}

func newNotifyDelivery(d *kernel.Deps, users auth.Service, m *mailer.Mailer, pages *httpx.Templates) *notifyDelivery {
	return &notifyDelivery{d: d, users: users, mail: m, tpl: m, pages: pages, tg: newTelegramClient(),
		now: time.Now, tests: map[string][]time.Time{}}
}

// register wires the cron, the job and the public endpoints.
func (s *notifyDelivery) register() {
	s.d.Services.Set(notifyServiceName, s)
	s.d.Jobs.Register(deliverJob, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.Run(ctx)
		return err
	}, 1)
	// The idempotency key names the 5-minute bucket, so several instances
	// enqueue one job per bucket and the queue's claim runs it once.
	s.d.Jobs.Cron(deliverJob, deliverEvery, func(ctx context.Context) error {
		bucket := s.now().UTC().Truncate(deliverEvery).Format("20060102T1504")
		_, err := s.d.Jobs.Enqueue(ctx, deliverJob, struct{}{}, jobs.Idempotency(deliverJob+":"+bucket))
		return err
	})
	s.d.Router.Webhooks.Post("/telegram/{path}", s.telegramWebhook)
	s.d.Router.Public.Get("/notifications/unsubscribe", s.unsubscribePage)
	s.d.Router.Public.Post("/notifications/unsubscribe", s.unsubscribeSubmit) // CSRF-exempt: the signed token is the credential
}

// runStats summarises one delivery run.
type runStats struct {
	Users, Sent, Failed int
}

// Run delivers pending notifications for at most deliverMaxUsers users,
// least recently checked first, and prunes old delivery records.
func (s *notifyDelivery) Run(ctx context.Context) (runStats, error) {
	start := s.now()
	now := start.UTC()
	var st runStats
	mailOK := s.mail.Configured(ctx)
	bot := s.loadBot(ctx)
	if !mailOK && !bot.configured() {
		return st, s.prune(ctx, now)
	}
	rows, err := s.d.DB.QueryContext(ctx, `SELECT `+prefixed("p.", prefsColumns)+` FROM notification_prefs p
		JOIN users u ON u.id = p.user_id
		WHERE u.status = ? AND ((? AND p.email_mode != 'off') OR (? AND p.telegram_enabled = 1 AND p.telegram_chat_id != ''))
		ORDER BY p.checked_at, p.user_id LIMIT ?`, auth.StatusActive, mailOK, bot.configured(), deliverMaxUsers)
	if err != nil {
		return st, err
	}
	var batch []notifyPrefs
	for rows.Next() {
		p, err := scanPrefs(rows)
		if err != nil {
			rows.Close()
			return st, err
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, p := range batch {
		if ctx.Err() != nil {
			break
		}
		sent, failed := s.deliverUser(ctx, p, now, mailOK, bot)
		st.Users++
		st.Sent += sent
		st.Failed += failed
		if _, err := s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET checked_at = ? WHERE user_id = ?`, db.FormatTime(now), p.UserID); err != nil {
			return st, err
		}
	}
	if st.Users > 0 {
		s.d.Log.Info("notify: delivery run", "users", st.Users, "sent", st.Sent, "failed", st.Failed, "duration_ms", time.Since(start).Milliseconds())
	}
	return st, s.prune(ctx, now)
}

func prefixed(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

func (s *notifyDelivery) prune(ctx context.Context, now time.Time) error {
	if _, err := s.d.DB.ExecContext(ctx, `DELETE FROM notification_deliveries WHERE delivered_at < ?`, db.FormatTime(now.Add(-deliverKeepFor))); err != nil {
		return err
	}
	_, err := s.d.DB.ExecContext(ctx, `DELETE FROM notification_telegram_codes WHERE expires_at < ?`, db.FormatTime(now.Add(-24*time.Hour)))
	return err
}

// deliverUser recomputes the user's items as that user (so the sources'
// permission checks run now) and sends what each channel has not had yet.
func (s *notifyDelivery) deliverUser(ctx context.Context, p notifyPrefs, now time.Time, mailOK bool, bot telegramBot) (sent, failed int) {
	u, err := s.users.Get(ctx, p.UserID)
	if err != nil || u.Status != auth.StatusActive {
		return 0, 0
	}
	uctx := i18n.WithLocale(auth.WithUser(ctx, u), userLocale(u))
	var items []categorized
	for _, n := range gatherNotifications(uctx, s.d) {
		if !p.IsMuted(n.Category) {
			items = append(items, n)
		}
	}
	count := func(ok bool, ch string) {
		metrics.CountNotification(ch, ok)
		if ok {
			sent++
		} else {
			failed++
		}
	}
	if mailOK && p.EmailMode == emailImmediate {
		if ok, tried := s.deliverEmail(uctx, u, p, items, false, now); tried {
			count(ok, channelEmail)
		}
	}
	if mailOK && p.EmailMode == emailDigest && now.Hour() == p.DigestHour && !sameUTCDay(p.LastDigestAt, now) {
		if ok, tried := s.deliverEmail(uctx, u, p, items, true, now); tried {
			count(ok, channelEmail)
		}
		if _, err := s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET last_digest_at = ? WHERE user_id = ?`, db.FormatTime(now), u.ID); err != nil {
			s.d.Log.Error("notify: digest mark", "user", u.ID, "err", err)
		}
	}
	if bot.configured() && p.TelegramEnabled && p.Linked() {
		if ok, tried := s.deliverTelegram(uctx, u, p, bot, items, now); tried {
			count(ok, channelTelegram)
		}
	}
	return sent, failed
}

func sameUTCDay(stamp string, now time.Time) bool {
	t, err := db.ParseTime(stamp)
	if err != nil {
		return false
	}
	return t.UTC().Format(time.DateOnly) == now.Format(time.DateOnly)
}

// since keeps items at or after the channel's start time.
func since(items []categorized, from string) []categorized {
	if from == "" {
		return items
	}
	var out []categorized
	for _, n := range items {
		if n.At >= from {
			out = append(out, n)
		}
	}
	return out
}

// claim records keys as being sent on channel and returns the items this
// call claimed: new keys, and keys whose earlier send failed fewer than
// deliverMaxAttempts times. A concurrent run cannot claim the same key: the
// insert or the conditional update affects a row for one caller only.
func (s *notifyDelivery) claim(ctx context.Context, userID, channel string, items []categorized, now time.Time) ([]categorized, error) {
	var out []categorized
	at := db.FormatTime(now)
	for _, n := range items {
		if len(out) == deliverMaxItems {
			break
		}
		res, err := s.d.DB.ExecContext(ctx, `INSERT INTO notification_deliveries (user_id, channel, key, status, attempts, delivered_at)
			VALUES (?, ?, ?, 'sending', 1, ?)
			ON CONFLICT (user_id, channel, key) DO UPDATE SET status = 'sending', attempts = notification_deliveries.attempts + 1,
				delivered_at = excluded.delivered_at
			WHERE notification_deliveries.status = 'failed' AND notification_deliveries.attempts < ?`,
			userID, channel, n.Key, at, deliverMaxAttempts)
		if err != nil {
			return out, err
		}
		if k, _ := res.RowsAffected(); k == 1 {
			out = append(out, n)
		}
	}
	return out, nil
}

// settle marks claimed keys sent or failed.
func (s *notifyDelivery) settle(ctx context.Context, userID, channel string, items []categorized, ok bool) {
	status := "sent"
	if !ok {
		status = "failed"
	}
	for _, n := range items {
		if _, err := s.d.DB.ExecContext(ctx, `UPDATE notification_deliveries SET status = ? WHERE user_id = ? AND channel = ? AND key = ?`,
			status, userID, channel, n.Key); err != nil {
			s.d.Log.Error("notify: settle", "channel", channel, "user", userID, "err", err)
		}
	}
}

// messageItem is one line of an email or Telegram message: a translated
// title, a truncated title-like detail and an absolute admin link.
type messageItem struct {
	Title, Detail, URL string
}

func (s *notifyDelivery) baseURL() string { return strings.TrimRight(s.d.Config.BaseURL, "/") }

func (s *notifyDelivery) messageItems(loc string, items []categorized) []messageItem {
	out := make([]messageItem, 0, len(items))
	for _, n := range items {
		title := s.d.I18n.T(loc, n.TitleKey)
		if n.Count > 0 {
			title = s.d.I18n.T(loc, n.TitleKey, n.Count)
		}
		out = append(out, messageItem{Title: title, Detail: truncateRunes(oneLineText(n.Detail), detailMaxRunes),
			URL: s.baseURL() + "/admin/notifications/go?key=" + url.QueryEscape(n.Key)})
	}
	return out
}

func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// emailData is the data of the notification email templates.
type emailData struct {
	Site, Heading, PrefsURL, UnsubscribeURL string
	Items                                   []messageItem
	Count                                   int
	Digest, Test                            bool
}

func (s *notifyDelivery) siteName(ctx context.Context) string {
	if t := strings.TrimSpace(s.d.Settings.GetString(ctx, "site.title", "")); t != "" {
		return truncateRunes(oneLineText(t), 60)
	}
	return "GoRun"
}

// renderEmail builds the subject and bodies in the user's admin language.
func (s *notifyDelivery) renderEmail(ctx context.Context, u *auth.User, items []messageItem, digest, test bool) (string, string, string, error) {
	loc := userLocale(u)
	data := emailData{Site: s.siteName(ctx), PrefsURL: s.baseURL() + notifyPrefsPage, UnsubscribeURL: s.unsubscribeURL(u.ID),
		Items: items, Count: len(items), Digest: digest, Test: test}
	switch {
	case test:
		data.Heading = s.d.I18n.T(loc, "notifydelivery.email.heading_test")
	case digest:
		data.Heading = s.d.I18n.T(loc, "notifydelivery.email.heading_digest", len(items))
	default:
		data.Heading = s.d.I18n.T(loc, "notifydelivery.email.heading", len(items))
	}
	return s.tpl.Render(loc, "notification", data)
}

// deliverEmail sends one email with the user's pending items. tried is false
// when there was nothing to send.
func (s *notifyDelivery) deliverEmail(ctx context.Context, u *auth.User, p notifyPrefs, items []categorized, digest bool, now time.Time) (ok, tried bool) {
	claimed, err := s.claim(ctx, u.ID, channelEmail, since(items, p.EmailSince), now)
	if err != nil {
		s.d.Log.Error("notify: claim", "channel", channelEmail, "user", u.ID, "err", err)
	}
	if len(claimed) == 0 {
		return false, false
	}
	subject, html, text, err := s.renderEmail(ctx, u, s.messageItems(userLocale(u), claimed), digest, false)
	if err == nil {
		err = s.mail.SendRequired(ctx, u.Email, subject, html, text)
	}
	if err != nil {
		s.d.Log.Warn("notify: send failed", "channel", channelEmail, "user", u.ID, "items", len(claimed), "err", err)
	}
	s.settle(ctx, u.ID, channelEmail, claimed, err == nil)
	return err == nil, true
}

// telegramText builds the plain-text message.
func (s *notifyDelivery) telegramText(ctx context.Context, loc string, items []messageItem, test bool) string {
	var b strings.Builder
	if test {
		b.WriteString(s.d.I18n.T(loc, "notifydelivery.email.heading_test"))
	} else {
		b.WriteString(s.siteName(ctx) + " · " + s.d.I18n.T(loc, "notifydelivery.email.heading", len(items)))
	}
	b.WriteString("\n")
	for _, it := range items {
		b.WriteString("\n• " + it.Title)
		if it.Detail != "" {
			b.WriteString("\n  " + it.Detail)
		}
		b.WriteString("\n  " + it.URL)
	}
	b.WriteString("\n\n" + s.d.I18n.T(loc, "notifydelivery.email.manage") + ": " + s.baseURL() + notifyPrefsPage)
	return b.String()
}

// deliverTelegram sends one message with the pending items. Three
// consecutive 403 answers (bot blocked) turn Telegram off for the user.
func (s *notifyDelivery) deliverTelegram(ctx context.Context, u *auth.User, p notifyPrefs, bot telegramBot, items []categorized, now time.Time) (ok, tried bool) {
	claimed, err := s.claim(ctx, u.ID, channelTelegram, since(items, p.TelegramSince), now)
	if err != nil {
		s.d.Log.Error("notify: claim", "channel", channelTelegram, "user", u.ID, "err", err)
	}
	if len(claimed) == 0 {
		return false, false
	}
	loc := userLocale(u)
	err = s.tg.send(ctx, bot.Token, p.ChatID, s.telegramText(ctx, loc, s.messageItems(loc, claimed), false))
	s.settle(ctx, u.ID, channelTelegram, claimed, err == nil)
	s.recordTelegramResult(ctx, u, err)
	return err == nil, true
}

// recordTelegramResult resets or counts consecutive 403s.
func (s *notifyDelivery) recordTelegramResult(ctx context.Context, u *auth.User, err error) {
	if err == nil {
		_, _ = s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET telegram_failures = 0 WHERE user_id = ? AND telegram_failures != 0`, u.ID)
		return
	}
	s.d.Log.Warn("notify: send failed", "channel", channelTelegram, "user", u.ID, "err", err.Error())
	te, isTG := err.(*telegramError)
	if !isTG || !te.blocked() {
		return
	}
	var n int
	if qerr := s.d.DB.QueryRowContext(ctx, `UPDATE notification_prefs SET telegram_failures = telegram_failures + 1 WHERE user_id = ? RETURNING telegram_failures`, u.ID).Scan(&n); qerr != nil {
		s.d.Log.Error("notify: telegram failures", "user", u.ID, "err", qerr)
		return
	}
	if n >= telegramBlockedLimit {
		if _, qerr := s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET telegram_enabled = 0, telegram_disabled = 'blocked', updated_at = ? WHERE user_id = ?`, db.Now(), u.ID); qerr != nil {
			s.d.Log.Error("notify: telegram disable", "user", u.ID, "err", qerr)
			return
		}
		s.d.Audit.Record(ctx, "notifications.telegram_disabled", "user", u.ID, map[string]any{"reason": "blocked", "failures": n})
		s.d.Log.Warn("notify: telegram disabled", "user", u.ID, "reason", "blocked")
	}
}

// allowTest applies the per-user, per-channel test-send limit.
func (s *notifyDelivery) allowTest(userID, channel string) bool {
	s.testMu.Lock()
	defer s.testMu.Unlock()
	now := s.now()
	key := userID + "|" + channel
	recent := s.tests[key][:0]
	for _, t := range s.tests[key] {
		if now.Sub(t) < testSendWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= testSendBurst {
		s.tests[key] = recent
		return false
	}
	s.tests[key] = append(recent, now)
	return true
}

// sendTest sends one sample message on channel to the user in ctx.
func (s *notifyDelivery) sendTest(ctx context.Context, u *auth.User, channel string) error {
	loc := userLocale(u)
	sample := []messageItem{{Title: s.d.I18n.T(loc, "notifydelivery.test.item"), URL: s.baseURL() + notifyPrefsPage}}
	var err error
	switch channel {
	case channelEmail:
		var subject, html, text string
		subject, html, text, err = s.renderEmail(ctx, u, sample, false, true)
		if err == nil {
			err = s.mail.SendRequired(ctx, u.Email, subject, html, text)
		}
	case channelTelegram:
		p, perr := loadPrefs(ctx, s.d.DB, u.ID)
		bot := s.loadBot(ctx)
		if perr != nil {
			return perr
		}
		if !bot.configured() || !p.Linked() {
			return errTelegramNotConfigured
		}
		err = s.tg.send(ctx, bot.Token, p.ChatID, s.telegramText(ctx, loc, sample, true))
		s.recordTelegramResult(ctx, u, err)
	default:
		return fmt.Errorf("notify: unknown channel %q", channel)
	}
	metrics.CountNotification(channel, err == nil)
	if err != nil {
		s.d.Log.Warn("notify: test send failed", "channel", channel, "user", u.ID, "err", err.Error())
	}
	return err
}

// Unsubscribe tokens turn email delivery off for one user without signing
// in. They are v1.<user id>.<HMAC-SHA256> under a key derived from the site
// secret for this purpose only, so they cannot be used for anything else.
func (s *notifyDelivery) unsubscribeKey() []byte {
	m := hmac.New(sha256.New, s.d.Config.SecretKey)
	m.Write([]byte("gorun/notifications/unsubscribe/v1"))
	return m.Sum(nil)
}

func (s *notifyDelivery) unsubscribeToken(userID string) string {
	m := hmac.New(sha256.New, s.unsubscribeKey())
	m.Write([]byte("unsubscribe-email:" + userID))
	return "v1." + base64.RawURLEncoding.EncodeToString([]byte(userID)) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *notifyDelivery) unsubscribeURL(userID string) string {
	return s.baseURL() + "/notifications/unsubscribe?token=" + url.QueryEscape(s.unsubscribeToken(userID))
}

// verifyUnsubscribe returns the user id of a valid token.
func (s *notifyDelivery) verifyUnsubscribe(token string) (string, bool) {
	if len(token) > 300 {
		return "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(raw) == 0 {
		return "", false
	}
	if !hmac.Equal([]byte(s.unsubscribeToken(string(raw))), []byte(token)) {
		return "", false
	}
	return string(raw), true
}
