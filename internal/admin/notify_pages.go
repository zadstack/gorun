package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

// notifyPrefsData is the preferences page (pages/notifications/preferences.html).
type notifyPrefsData struct {
	Prefs          notifyPrefs
	MailConfigured bool
	BotConfigured  bool
	BotUsername    string
	CanSettings    bool
	Categories     []string
	Hours          []int
	EmailModes     []string
	Fields         map[string]string
	// LinkCode and DeepLink are shown once, right after Link Telegram.
	LinkCode, DeepLink string
	LinkExpires        string
}

// notifySettingsData is the site page (pages/notifications/settings.html).
type notifySettingsData struct {
	TokenSet         bool
	Username         string
	WebhookAt        string
	WebhookReady     bool
	MailConfigured   bool
	EmailUsers       int
	TelegramUsers    int
	Fields           map[string]string
	BaseURLIsHTTPS   bool
	WebhookAvailable bool
}

func (h *handlers) notifyDeliveryRoutes() {
	r := h.d.Router.Admin
	r.Get("/account/notifications", h.notifyPrefsPage)
	r.Post("/account/notifications", h.notifyPrefsSave)
	r.Post("/account/notifications/test/{channel}", h.notifyTest)
	r.Post("/account/notifications/telegram/link", h.notifyTelegramLink)
	r.Post("/account/notifications/telegram/unlink", h.notifyTelegramUnlink)
	st := r.With(auth.RequirePerm(PermSettings))
	st.Get("/settings/notifications", h.notifySettingsPage)
	st.Post("/settings/notifications", h.notifySettingsSave)
	st.Post("/settings/notifications/webhook", h.notifySettingsWebhook)
	st.Post("/settings/notifications/remove", h.notifySettingsRemove)
}

func hours() []int {
	out := make([]int, 24)
	for i := range out {
		out[i] = i
	}
	return out
}

func (h *handlers) notifyPrefsView(ctx context.Context, u *auth.User) (notifyPrefsData, error) {
	p, err := loadPrefs(ctx, h.d.DB, u.ID)
	if err != nil {
		return notifyPrefsData{}, err
	}
	bot := h.notify.loadBot(ctx)
	return notifyPrefsData{Prefs: p, MailConfigured: h.notify.mail.Configured(ctx), BotConfigured: bot.configured(),
		BotUsername: bot.Username, CanSettings: auth.Can(ctx, PermSettings), Categories: visibleCategories(ctx, h.d),
		Hours: hours(), EmailModes: []string{emailOff, emailImmediate, emailDigest}, Fields: map[string]string{}}, nil
}

func (h *handlers) renderPrefs(w http.ResponseWriter, r *http.Request, data notifyPrefsData, status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/notifications/preferences.html", data)
}

func (h *handlers) notifyPrefsPage(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	data, err := h.notifyPrefsView(r.Context(), u)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderPrefs(w, r, data, http.StatusOK)
}

// notifyPrefsSave stores the email mode, digest hour, Telegram toggle and
// category mutes. Turning a channel on starts it now: older items are not
// sent as a backlog.
func (h *handlers) notifyPrefsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	data, err := h.notifyPrefsView(ctx, u)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	p := data.Prefs
	now := db.FormatTime(h.notify.now().UTC())
	mode := r.FormValue("email_mode")
	if !slices.Contains([]string{emailOff, emailImmediate, emailDigest}, mode) {
		data.Fields["email_mode"] = "notifydelivery.error.mode"
	} else if mode != emailOff && mode != p.EmailMode && !data.MailConfigured {
		data.Fields["email_mode"] = "notifydelivery.email.not_configured"
	}
	hour, herr := strconv.Atoi(r.FormValue("digest_hour"))
	if herr != nil || hour < 0 || hour > 23 {
		data.Fields["digest_hour"] = "notifydelivery.error.hour"
	}
	if len(data.Fields) > 0 {
		h.renderPrefs(w, r, data, http.StatusUnprocessableEntity)
		return
	}
	if p.EmailMode == emailOff && mode != emailOff {
		p.EmailSince = now
	}
	p.EmailMode, p.DigestHour = mode, hour
	tg := r.FormValue("telegram_enabled") == "on"
	if tg && !p.Linked() {
		tg = false
	}
	if tg && !p.TelegramEnabled {
		p.TelegramSince, p.TelegramDisabled, p.TelegramFailures = now, "", 0
	}
	p.TelegramEnabled = tg
	// Only the categories shown on the page change; others keep their state.
	muted := slices.DeleteFunc(slices.Clone(p.Muted), func(c string) bool { return slices.Contains(data.Categories, c) })
	for _, c := range data.Categories {
		if r.FormValue("deliver_"+c) != "on" {
			muted = append(muted, c)
		}
	}
	p.Muted = cleanCategories(muted)
	if err := savePrefs(ctx, h.d.DB, p); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "notifications.preferences_updated", "user", u.ID,
		map[string]any{"email_mode": p.EmailMode, "digest_hour": p.DigestHour, "telegram": p.TelegramEnabled, "muted": p.Muted})
	httpx.SetFlash(w, "success", "notifydelivery.saved")
	http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
}

func (h *handlers) notifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	channel := chi.URLParam(r, "channel")
	if u == nil || (channel != channelEmail && channel != channelTelegram) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if channel == channelEmail && !h.notify.mail.Configured(ctx) {
		httpx.SetFlash(w, "error", "notifydelivery.email.not_configured")
		http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
		return
	}
	if !h.notify.allowTest(u.ID, channel) {
		w.Header().Set("Retry-After", strconv.Itoa(int(testSendWindow/time.Second)))
		httpx.SetFlash(w, "error", "notifydelivery.test.rate_limited")
		http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
		return
	}
	if err := h.notify.sendTest(ctx, u, channel); err != nil {
		h.d.Audit.Record(ctx, "notifications.test_failed", "user", u.ID, map[string]any{"channel": channel})
		httpx.SetFlash(w, "error", "notifydelivery.test.failed")
	} else {
		h.d.Audit.Record(ctx, "notifications.test_sent", "user", u.ID, map[string]any{"channel": channel})
		httpx.SetFlash(w, "success", "notifydelivery.test.sent")
	}
	http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
}

// notifyTelegramLink creates a one-time code and shows it once.
func (h *handlers) notifyTelegramLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	data, err := h.notifyPrefsView(ctx, u)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !data.BotConfigured {
		httpx.SetFlash(w, "error", "notifydelivery.telegram.not_configured")
		http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
		return
	}
	code, expires, err := h.notify.newLinkCode(ctx, u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	data.LinkCode = code
	data.DeepLink = "https://t.me/" + data.BotUsername + "?start=" + code
	data.LinkExpires = db.FormatTime(expires)
	h.renderPrefs(w, r, data, http.StatusOK)
}

func (h *handlers) notifyTelegramUnlink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if err := h.notify.unlinkTelegram(ctx, u.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "notifications.telegram_unlinked", "user", u.ID, nil)
	httpx.SetFlash(w, "success", "notifydelivery.telegram.unlinked")
	http.Redirect(w, r, notifyPrefsPage, http.StatusSeeOther)
}

// Site settings: the Telegram bot.

const notifySettingsPath = "/admin/settings/notifications"

func (h *handlers) notifySettingsView(ctx context.Context) (notifySettingsData, error) {
	data := notifySettingsData{TokenSet: h.notify.tokenSet(ctx), Username: h.d.Settings.GetString(ctx, settingTelegramUsername, ""),
		WebhookAt: h.d.Settings.GetString(ctx, settingTelegramWebhookAt, ""), MailConfigured: h.notify.mail.Configured(ctx),
		Fields: map[string]string{}, BaseURLIsHTTPS: strings.HasPrefix(h.d.Config.BaseURL, "https://")}
	data.WebhookReady = data.TokenSet && data.WebhookAt != ""
	data.WebhookAvailable = data.TokenSet && data.BaseURLIsHTTPS
	err := h.d.DB.QueryRowContext(ctx, `SELECT
		coalesce(sum(email_mode != 'off'), 0), coalesce(sum(telegram_enabled = 1 AND telegram_chat_id != ''), 0) FROM notification_prefs`).
		Scan(&data.EmailUsers, &data.TelegramUsers)
	return data, err
}

func (h *handlers) notifySettingsPage(w http.ResponseWriter, r *http.Request) {
	data, err := h.notifySettingsView(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.Render(w, r, "pages/notifications/settings.html", data)
}

// notifySettingsSave stores the bot username and, when given, a new token.
// The token is never echoed back, also not on a validation error.
func (h *handlers) notifySettingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, err := h.notifySettingsView(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	token := strings.TrimSpace(r.FormValue("bot_token"))
	username := strings.TrimPrefix(strings.TrimSpace(r.FormValue("bot_username")), "@")
	if token != "" && !telegramTokenRe.MatchString(token) {
		data.Fields["bot_token"] = "notifydelivery.settings.token_invalid"
	}
	if !telegramUsernameRe.MatchString(username) {
		data.Fields["bot_username"] = "notifydelivery.settings.username_invalid"
	}
	if token == "" && !data.TokenSet {
		data.Fields["bot_token"] = "notifydelivery.settings.token_required"
	}
	if len(data.Fields) > 0 {
		data.Username = username
		w.Header().Set("Cache-Control", "private, no-store")
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/notifications/settings.html", data)
		return
	}
	if err := h.notify.saveBot(ctx, token, username); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "notifications.telegram_bot_updated", "setting", "notify.telegram",
		map[string]any{"bot_username": username, "token_changed": token != ""})
	httpx.SetFlash(w, "success", "notifydelivery.settings.saved")
	http.Redirect(w, r, notifySettingsPath, http.StatusSeeOther)
}

func (h *handlers) notifySettingsWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := h.notify.registerWebhook(ctx)
	switch {
	case err == nil:
		h.d.Audit.Record(ctx, "notifications.telegram_webhook_registered", "setting", "notify.telegram", nil)
		httpx.SetFlash(w, "success", "notifydelivery.settings.webhook_ok")
	case errors.Is(err, errTelegramNeedsHTTPS):
		httpx.SetFlash(w, "error", "notifydelivery.settings.webhook_https")
	case errors.Is(err, errTelegramNotConfigured):
		httpx.SetFlash(w, "error", "notifydelivery.telegram.not_configured")
	default:
		h.d.Log.Warn("notify: telegram setWebhook failed", "err", err.Error())
		httpx.SetFlash(w, "error", "notifydelivery.settings.webhook_failed")
	}
	http.Redirect(w, r, notifySettingsPath, http.StatusSeeOther)
}

func (h *handlers) notifySettingsRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.notify.removeBot(ctx); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "notifications.telegram_bot_removed", "setting", "notify.telegram", nil)
	httpx.SetFlash(w, "success", "notifydelivery.settings.removed")
	http.Redirect(w, r, notifySettingsPath, http.StatusSeeOther)
}

// Public unsubscribe page. GET only shows a confirmation (link scanners must
// not change anything); POST with the token turns email off.

type unsubscribeData struct {
	Token          string
	Done, Invalid  bool
	Email          string
	PreferencesURL string
}

func (s *notifyDelivery) unsubscribeRender(w http.ResponseWriter, r *http.Request, status int, data unsubscribeData) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	data.PreferencesURL = notifyPrefsPage
	s.pages.RenderWith(w, r, status, "auth", "pages/notifications/unsubscribe.html", data)
}

// unsubscribeUser resolves the token and puts the user's language on r.
func (s *notifyDelivery) unsubscribeUser(r *http.Request) (*auth.User, *http.Request, bool) {
	id, ok := s.verifyUnsubscribe(r.FormValue("token"))
	if !ok {
		return nil, r, false
	}
	u, err := s.users.Get(r.Context(), id)
	if err != nil {
		return nil, r, false
	}
	return u, r.WithContext(i18n.WithLocale(r.Context(), userLocale(u))), true
}

func (s *notifyDelivery) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	u, r, ok := s.unsubscribeUser(r)
	if !ok {
		s.unsubscribeRender(w, r, http.StatusBadRequest, unsubscribeData{Invalid: true})
		return
	}
	s.unsubscribeRender(w, r, http.StatusOK, unsubscribeData{Token: r.FormValue("token"), Email: maskEmail(u.Email)})
}

func (s *notifyDelivery) unsubscribeSubmit(w http.ResponseWriter, r *http.Request) {
	u, r, ok := s.unsubscribeUser(r)
	if !ok {
		s.unsubscribeRender(w, r, http.StatusBadRequest, unsubscribeData{Invalid: true})
		return
	}
	ctx := r.Context()
	if _, err := s.d.DB.ExecContext(ctx, `UPDATE notification_prefs SET email_mode = 'off', updated_at = ? WHERE user_id = ? AND email_mode != 'off'`,
		db.Now(), u.ID); err != nil {
		s.d.Log.Error("notify: unsubscribe", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	s.d.Audit.Record(auth.WithUser(ctx, u), "notifications.email_unsubscribed", "user", u.ID, map[string]any{"via": "email_link"})
	s.unsubscribeRender(w, r, http.StatusOK, unsubscribeData{Done: true, Email: maskEmail(u.Email)})
}
