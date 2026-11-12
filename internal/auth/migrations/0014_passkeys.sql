-- Passkeys (WebAuthn) as a second factor. docs/specs/passkeys.md.
--
-- webauthn_users: one random 64-byte WebAuthn user handle per user (never
-- the user id or email), shared by all of the user's passkeys.
CREATE TABLE webauthn_users (
    user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    handle     BLOB NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

-- user_passkeys: the credential record. rp_id partitions credentials by the
-- Relying Party ID they were created for (the host of GORUN_BASE_URL);
-- credential_id is unique across all users (WebAuthn L3 §7.1 step 26).
-- flags is the authenticator flag octet (UV, BE, BS). sign_count is written
-- back after every successful sign-in; a counter that does not grow is
-- refused as a possible cloned authenticator.
CREATE TABLE user_passkeys (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rp_id              TEXT NOT NULL,
    credential_id      BLOB NOT NULL UNIQUE,
    public_key         BLOB NOT NULL,
    sign_count         INTEGER NOT NULL DEFAULT 0,
    transports         TEXT NOT NULL DEFAULT '',
    aaguid             BLOB,
    flags              INTEGER NOT NULL DEFAULT 0,
    attestation_type   TEXT NOT NULL DEFAULT '',
    attestation_format TEXT NOT NULL DEFAULT '',
    name               TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    last_used_at       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX user_passkeys_user ON user_passkeys (user_id, rp_id);

-- webauthn_challenges: an open registration or sign-in ceremony. Only the
-- SHA-256 of the challenge is stored (session_data holds the rest of the
-- library's SessionData without it). binding is the session id (register) or
-- the pending sign-in's token hash (login); a row is deleted when used and
-- expires after five minutes.
CREATE TABLE webauthn_challenges (
    challenge_hash TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,
    user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    binding        TEXT NOT NULL,
    session_data   TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    created_at     TEXT NOT NULL
);
CREATE INDEX webauthn_challenges_binding ON webauthn_challenges (kind, binding);
CREATE INDEX webauthn_challenges_expires ON webauthn_challenges (expires_at);
