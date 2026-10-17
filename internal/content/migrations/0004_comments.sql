-- Visitor comments on content items whose type supports "comments". The body is plain text
-- (escaped on output); a reply's parent is an approved top-level comment of the same item.
CREATE TABLE comments (
    id           TEXT PRIMARY KEY,
    content_id   TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    parent_id    TEXT REFERENCES comments (id) ON DELETE CASCADE,
    author_name  TEXT NOT NULL,
    author_email TEXT NOT NULL DEFAULT '',
    user_id      TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'spam', 'trash')),
    ip           TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX comments_content ON comments (content_id, status, created_at);
CREATE INDEX comments_status ON comments (status, created_at);
