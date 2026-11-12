-- Delivery of the derived admin notifications by email and Telegram
-- (internal/admin/notify_*.go, docs/NOTIFICATIONS.md). Notifications stay
-- derived; these tables hold per-user preferences, which keys were already
-- delivered on which channel (the outbox dedupe), and one-time Telegram
-- link codes (hashed).
CREATE TABLE notification_prefs (
    user_id            TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    email_mode         TEXT NOT NULL DEFAULT 'off' CHECK (email_mode IN ('off', 'immediate', 'digest')),
    digest_hour        INTEGER NOT NULL DEFAULT 7 CHECK (digest_hour BETWEEN 0 AND 23),
    email_since        TEXT NOT NULL DEFAULT '',
    last_digest_at     TEXT NOT NULL DEFAULT '',
    telegram_enabled   INTEGER NOT NULL DEFAULT 0,
    telegram_since     TEXT NOT NULL DEFAULT '',
    telegram_chat_id   TEXT NOT NULL DEFAULT '',
    telegram_linked_at TEXT NOT NULL DEFAULT '',
    telegram_failures  INTEGER NOT NULL DEFAULT 0,
    telegram_disabled  TEXT NOT NULL DEFAULT '',
    muted              TEXT NOT NULL DEFAULT '[]',
    checked_at         TEXT NOT NULL DEFAULT '',
    updated_at         TEXT NOT NULL
);
CREATE INDEX notification_prefs_checked ON notification_prefs (checked_at);

CREATE TABLE notification_deliveries (
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel      TEXT NOT NULL CHECK (channel IN ('email', 'telegram')),
    key          TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('sending', 'sent', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 1,
    delivered_at TEXT NOT NULL,
    PRIMARY KEY (user_id, channel, key)
);
CREATE INDEX notification_deliveries_at ON notification_deliveries (delivered_at);

CREATE TABLE notification_telegram_codes (
    code_hash  TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX notification_telegram_codes_user ON notification_telegram_codes (user_id);
