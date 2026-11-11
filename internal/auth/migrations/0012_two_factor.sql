-- TOTP second factor (RFC 6238). secret_enc is the 20-byte shared secret
-- sealed with AES-256-GCM under the instance secret key (db.Encrypt, AAD
-- "auth.totp:<user_id>"), so a database copy alone cannot mint codes.
-- enabled_at is '' while enrolment waits for the first valid code.
-- last_step is the last accepted 30-second time step: a code is accepted only
-- for a later step, so a seen code cannot be replayed.
CREATE TABLE user_totp (
    user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret_enc TEXT NOT NULL,
    enabled_at TEXT NOT NULL DEFAULT '',
    last_step  INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Single-use recovery codes. code_hash is SHA-256 (hex) of the user id and
-- the normalized code; codes carry 50 random bits, so a fast hash is enough
-- (as for session tokens and API keys).
CREATE TABLE user_recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX user_recovery_codes_user ON user_recovery_codes (user_id);

-- A password (or magic link) sign-in waiting for its second factor. Only the
-- SHA-256 of the cookie token is stored; it expires after five minutes and is
-- deleted when used or after too many wrong codes.
CREATE TABLE login_challenges (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    method     TEXT NOT NULL,
    next       TEXT NOT NULL DEFAULT '',
    attempts   INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX login_challenges_expires ON login_challenges (expires_at);
