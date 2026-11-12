package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// Telegram bot settings. The token and the webhook secret are encrypted with
// db.Encrypt (AAD "settings:<key>") and never rendered.
const (
	settingTelegramToken     = "notify.telegram.bot_token"
	settingTelegramUsername  = "notify.telegram.bot_username"
	settingTelegramSecret    = "notify.telegram.webhook_secret"
	settingTelegramWebhookAt = "notify.telegram.webhook_at"

	telegramAPI          = "https://api.telegram.org"
	telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"
	telegramMaxBody      = 64 << 10
	telegramMaxText      = 4000
	telegramAttempts     = 3
	telegramMaxWait      = 10 * time.Second
	telegramBlockedLimit = 3
	linkCodeTTL          = 15 * time.Minute
)

var (
	telegramTokenRe    = regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{20,100}$`)
	telegramUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,29}[Bb][Oo][Tt]$`)
	linkCodeRe         = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
)

// telegramBot is the configured site bot. Token is the decrypted secret and
// must never reach a template, a log line or an error message.
type telegramBot struct {
	Token, Username string
}

func (b telegramBot) configured() bool { return b.Token != "" && b.Username != "" }

// telegramClient calls the Bot API through the SSRF-safe client (private
// addresses refused, no redirects). base is api.telegram.org outside tests.
type telegramClient struct {
	base  string
	http  *http.Client
	sleep func(context.Context, time.Duration) error
}

func newTelegramClient() *telegramClient {
	return &telegramClient{
		base:  telegramAPI,
		http:  httpx.NewSafeClientOpts(httpx.SafeClientOptions{NoRedirects: true, Timeout: 15 * time.Second}),
		sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// telegramError is a Bot API failure. Its text never contains the token.
type telegramError struct {
	Status      int
	Description string
	RetryAfter  int
}

func (e *telegramError) Error() string {
	if e.Status == 0 {
		return "telegram: " + e.Description
	}
	return fmt.Sprintf("telegram: HTTP %d: %s", e.Status, e.Description)
}

// blocked reports a 403: the user blocked the bot or never started it.
func (e *telegramError) blocked() bool { return e.Status == http.StatusForbidden }

func (e *telegramError) retryable() bool {
	return e.Status == 0 || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// call performs one Bot API request. The token is validated before it is
// placed in the path, so it cannot change the host or the path.
func (c *telegramClient) call(ctx context.Context, token, method string, payload any) error {
	if !telegramTokenRe.MatchString(token) {
		return &telegramError{Description: "invalid bot token"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return &telegramError{Description: "request"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error repeats the URL, which holds the token: keep only the class.
		desc := "transport error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			desc = "timeout"
		}
		return &telegramError{Description: desc}
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out)
	if resp.StatusCode == http.StatusOK && out.OK {
		return nil
	}
	desc := strings.ReplaceAll(out.Description, token, "<redacted>")
	if len(desc) > 200 {
		desc = desc[:200]
	}
	return &telegramError{Status: resp.StatusCode, Description: desc, RetryAfter: out.Parameters.RetryAfter}
}

// send posts a plain-text message, retrying 429, 5xx and transport errors
// with backoff (1 s, 2 s, or the API's retry_after up to telegramMaxWait).
func (c *telegramClient) send(ctx context.Context, token, chatID, text string) error {
	if r := []rune(text); len(r) > telegramMaxText {
		text = string(r[:telegramMaxText-1]) + "…"
	}
	payload := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	var err error
	for attempt := 1; attempt <= telegramAttempts; attempt++ {
		err = c.call(ctx, token, "sendMessage", payload)
		var te *telegramError
		if err == nil || !errors.As(err, &te) || !te.retryable() || attempt == telegramAttempts {
			return err
		}
		wait := time.Duration(attempt) * time.Second
		if te.RetryAfter > 0 {
			wait = min(time.Duration(te.RetryAfter)*time.Second, telegramMaxWait)
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return err
		}
	}
	return err
}

// loadBot reads the bot settings; a token that cannot be decrypted counts as
// not configured (and is logged without its value).
func (s *notifyDelivery) loadBot(ctx context.Context) telegramBot {
	b := telegramBot{Username: s.d.Settings.GetString(ctx, settingTelegramUsername, "")}
	if enc := s.d.Settings.GetString(ctx, settingTelegramToken, ""); enc != "" {
		plain, err := db.Decrypt(s.d.Config.SecretKey, enc, "settings:"+settingTelegramToken)
		if err != nil {
			s.d.Log.Error("notify: telegram token unreadable", "err", err)
			return telegramBot{Username: b.Username}
		}
		b.Token = string(plain)
	}
	return b
}

// tokenSet reports whether a bot token is stored, without decrypting it.
func (s *notifyDelivery) tokenSet(ctx context.Context) bool {
	return s.d.Settings.GetString(ctx, settingTelegramToken, "") != ""
}

// webhookSecret returns the raw webhook secret, or nil when none is stored.
func (s *notifyDelivery) webhookSecret(ctx context.Context) []byte {
	enc := s.d.Settings.GetString(ctx, settingTelegramSecret, "")
	if enc == "" {
		return nil
	}
	plain, err := db.Decrypt(s.d.Config.SecretKey, enc, "settings:"+settingTelegramSecret)
	if err != nil {
		return nil
	}
	return plain
}

func derive(secret []byte, purpose string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(purpose))
	return hex.EncodeToString(m.Sum(nil))
}

// webhookPath and webhookHeader are both derived from the secret: the path
// keeps the endpoint unguessable, the header is what Telegram sends back.
func webhookPath(secret []byte) string   { return derive(secret, "gorun/telegram/path")[:40] }
func webhookHeader(secret []byte) string { return derive(secret, "gorun/telegram/header") }

func constEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// saveBot stores a new token (when given) and username. A new token rotates
// the webhook secret, so the webhook has to be registered again.
func (s *notifyDelivery) saveBot(ctx context.Context, token, username string) error {
	if token != "" {
		enc, err := db.Encrypt(s.d.Config.SecretKey, []byte(token), "settings:"+settingTelegramToken)
		if err != nil {
			return err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		sec, err := db.Encrypt(s.d.Config.SecretKey, raw, "settings:"+settingTelegramSecret)
		if err != nil {
			return err
		}
		if err := s.d.Settings.Set(ctx, settingTelegramToken, enc); err != nil {
			return err
		}
		if err := s.d.Settings.Set(ctx, settingTelegramSecret, sec); err != nil {
			return err
		}
		if err := s.d.Settings.Delete(ctx, settingTelegramWebhookAt); err != nil {
			return err
		}
	}
	return s.d.Settings.Set(ctx, settingTelegramUsername, username)
}

// removeBot deletes the token, the webhook secret and the webhook state.
func (s *notifyDelivery) removeBot(ctx context.Context) error {
	for _, k := range []string{settingTelegramToken, settingTelegramSecret, settingTelegramWebhookAt} {
		if err := s.d.Settings.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// registerWebhook points the bot's webhook at this site.
func (s *notifyDelivery) registerWebhook(ctx context.Context) error {
	bot := s.loadBot(ctx)
	secret := s.webhookSecret(ctx)
	if bot.Token == "" || secret == nil {
		return errTelegramNotConfigured
	}
	base := strings.TrimRight(s.d.Config.BaseURL, "/")
	if !strings.HasPrefix(base, "https://") {
		return errTelegramNeedsHTTPS
	}
	err := s.tg.call(ctx, bot.Token, "setWebhook", map[string]any{
		"url": base + "/webhooks/telegram/" + webhookPath(secret), "secret_token": webhookHeader(secret),
		"allowed_updates": []string{"message"}, "drop_pending_updates": true,
	})
	if err != nil {
		return err
	}
	return s.d.Settings.Set(ctx, settingTelegramWebhookAt, db.Now())
}

var (
	errTelegramNotConfigured = errors.New("telegram: bot not configured")
	errTelegramNeedsHTTPS    = errors.New("telegram: webhook needs an https base URL")
)

// newLinkCode replaces the user's unused codes with a fresh one; only its
// SHA-256 is stored.
func (s *notifyDelivery) newLinkCode(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.now().UTC().Add(linkCodeTTL)
	err := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notification_telegram_codes WHERE user_id = ? AND used_at = ''`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO notification_telegram_codes (code_hash, user_id, expires_at) VALUES (?, ?, ?)`,
			hashCode(code), userID, db.FormatTime(expires))
		return err
	})
	return code, expires, err
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// consumeLinkCode marks an unexpired, unused code used and returns its user.
func (s *notifyDelivery) consumeLinkCode(ctx context.Context, code string) (string, bool) {
	if !linkCodeRe.MatchString(code) {
		return "", false
	}
	now := db.FormatTime(s.now().UTC())
	var userID string
	err := s.d.DB.QueryRowContext(ctx, `UPDATE notification_telegram_codes SET used_at = ?
		WHERE code_hash = ? AND used_at = '' AND expires_at > ? RETURNING user_id`, now, hashCode(code), now).Scan(&userID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.d.Log.Error("notify: link code", "err", err)
		}
		return "", false
	}
	return userID, true
}

// linkTelegram stores chatID for the user and enables delivery from now.
func (s *notifyDelivery) linkTelegram(ctx context.Context, userID, chatID string) error {
	now := db.FormatTime(s.now().UTC())
	_, err := s.d.DB.ExecContext(ctx, `INSERT INTO notification_prefs (user_id, telegram_enabled, telegram_since, telegram_chat_id, telegram_linked_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET telegram_enabled = 1, telegram_since = excluded.telegram_since,
			telegram_chat_id = excluded.telegram_chat_id, telegram_linked_at = excluded.telegram_linked_at,
			telegram_failures = 0, telegram_disabled = '', updated_at = excluded.updated_at`,
		userID, now, chatID, now, now)
	return err
}

// unlinkTelegram forgets the chat and turns Telegram delivery off.
func (s *notifyDelivery) unlinkTelegram(ctx context.Context, userID string) error {
	_, err := s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET telegram_enabled = 0, telegram_chat_id = '', telegram_linked_at = '',
		telegram_failures = 0, telegram_disabled = '', updated_at = ? WHERE user_id = ?`, db.Now(), userID)
	return err
}

// telegramUpdate is the part of a Bot API Update the webhook reads.
type telegramUpdate struct {
	Message *struct {
		Text string `json:"text"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			LanguageCode string `json:"language_code"`
		} `json:"from"`
	} `json:"message"`
}

// telegramWebhook receives Bot API updates. The path and the secret header
// are both derived from the stored webhook secret and compared in constant
// time; anything else is 404 (no bot or wrong path) or 401 (wrong header).
// It handles "/start <code>" in private chats and answers with a sendMessage
// method in the response body, so linking needs no outbound call.
func (s *notifyDelivery) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	secret := s.webhookSecret(ctx)
	if secret == nil || !s.tokenSet(ctx) || !constEq(chi.URLParam(r, "path"), webhookPath(secret)) {
		http.NotFound(w, r)
		return
	}
	if !constEq(r.Header.Get(telegramSecretHeader), webhookHeader(secret)) {
		s.d.Log.Warn("notify: telegram webhook rejected", "reason", "secret header")
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	var upd telegramUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, telegramMaxBody)).Decode(&upd); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	m := upd.Message
	if m == nil || m.Chat.Type != "private" || !strings.HasPrefix(m.Text, "/start") {
		w.WriteHeader(http.StatusOK)
		return
	}
	loc := "en"
	if strings.HasPrefix(m.From.LanguageCode, "ar") {
		loc = "ar"
	}
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	reply := s.d.I18n.T(loc, "notifydelivery.telegram.reply_invalid")
	fields := strings.Fields(m.Text)
	if len(fields) == 2 {
		if userID, ok := s.consumeLinkCode(ctx, fields[1]); ok {
			if u, err := s.users.Get(ctx, userID); err == nil && s.linkTelegram(ctx, userID, chatID) == nil {
				reply = s.d.I18n.T(userLocale(u), "notifydelivery.telegram.reply_linked")
				s.d.Audit.Record(auth.WithUser(ctx, u), "notifications.telegram_linked", "user", u.ID, map[string]any{"via": "webhook"})
				s.d.Log.Info("notify: telegram linked", "user", u.ID)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"method": "sendMessage", "chat_id": m.Chat.ID, "text": reply})
}
