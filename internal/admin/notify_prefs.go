package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// Email delivery modes (notification_prefs.email_mode).
const (
	emailOff       = "off"
	emailImmediate = "immediate"
	emailDigest    = "digest"
)

// Delivery channels (notification_deliveries.channel).
const (
	channelEmail    = "email"
	channelTelegram = "telegram"
)

const defaultDigestHour = 7

// notifyPrefs is one user's delivery preferences and Telegram link.
type notifyPrefs struct {
	UserID           string
	EmailMode        string
	DigestHour       int
	EmailSince       string
	LastDigestAt     string
	TelegramEnabled  bool
	TelegramSince    string
	ChatID           string
	LinkedAt         string
	TelegramFailures int
	TelegramDisabled string
	Muted            []string
	CheckedAt        string
}

// Linked reports whether a Telegram chat is linked.
func (p notifyPrefs) Linked() bool { return p.ChatID != "" }

// IsMuted reports whether delivery of category is muted.
func (p notifyPrefs) IsMuted(category string) bool { return slices.Contains(p.Muted, category) }

const prefsColumns = `user_id, email_mode, digest_hour, email_since, last_digest_at, telegram_enabled, telegram_since,
	telegram_chat_id, telegram_linked_at, telegram_failures, telegram_disabled, muted, checked_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanPrefs(r rowScanner) (notifyPrefs, error) {
	var p notifyPrefs
	var muted string
	err := r.Scan(&p.UserID, &p.EmailMode, &p.DigestHour, &p.EmailSince, &p.LastDigestAt, &p.TelegramEnabled, &p.TelegramSince,
		&p.ChatID, &p.LinkedAt, &p.TelegramFailures, &p.TelegramDisabled, &muted, &p.CheckedAt)
	if err != nil {
		return p, err
	}
	if json.Unmarshal([]byte(muted), &p.Muted) != nil {
		p.Muted = nil
	}
	return p, nil
}

// loadPrefs returns the stored preferences, or the defaults (everything off)
// when the user never saved any.
func loadPrefs(ctx context.Context, q *sql.DB, userID string) (notifyPrefs, error) {
	p, err := scanPrefs(q.QueryRowContext(ctx, `SELECT `+prefsColumns+` FROM notification_prefs WHERE user_id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return notifyPrefs{UserID: userID, EmailMode: emailOff, DigestHour: defaultDigestHour}, nil
	}
	return p, err
}

// savePrefs upserts the user-editable fields and the channel start times.
func savePrefs(ctx context.Context, q *sql.DB, p notifyPrefs) error {
	muted, _ := json.Marshal(cleanCategories(p.Muted))
	_, err := q.ExecContext(ctx, `INSERT INTO notification_prefs (user_id, email_mode, digest_hour, email_since, telegram_enabled, telegram_since,
			telegram_disabled, telegram_failures, muted, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email_mode = excluded.email_mode, digest_hour = excluded.digest_hour,
			email_since = excluded.email_since, telegram_enabled = excluded.telegram_enabled, telegram_since = excluded.telegram_since,
			telegram_disabled = excluded.telegram_disabled, telegram_failures = excluded.telegram_failures,
			muted = excluded.muted, updated_at = excluded.updated_at`,
		p.UserID, p.EmailMode, p.DigestHour, p.EmailSince, p.TelegramEnabled, p.TelegramSince, p.TelegramDisabled, p.TelegramFailures,
		string(muted), db.Now())
	return err
}

// cleanCategories keeps known, mutable categories once each.
func cleanCategories(in []string) []string {
	out := []string{}
	for _, c := range kernel.NotifyCategories {
		if slices.Contains(in, c) {
			out = append(out, c)
		}
	}
	return out
}

// visibleCategories lists the mutable categories with at least one source the
// user in ctx may see, in display order.
func visibleCategories(ctx context.Context, d *kernel.Deps) []string {
	seen := map[string]bool{}
	for _, src := range d.Admin.NotificationSources() {
		if auth.Can(ctx, src.Permission) {
			seen[src.Category] = true
		}
	}
	var out []string
	for _, c := range kernel.NotifyCategories {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// userLocale is the admin language used for a user's messages.
func userLocale(u *auth.User) string {
	if u != nil {
		switch l := strings.ToLower(u.Locale); {
		case strings.HasPrefix(l, "ar"):
			return "ar"
		case strings.HasPrefix(l, "en"):
			return "en"
		}
	}
	return i18n.DefaultLocale
}
