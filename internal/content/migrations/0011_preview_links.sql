-- Shareable preview links. Only the SHA-256 of a token is stored; the token
-- itself is shown once, when the link is created.
CREATE TABLE content_preview_links (
    id         TEXT PRIMARY KEY,
    content_id TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE INDEX content_preview_links_content ON content_preview_links (content_id, expires_at);
