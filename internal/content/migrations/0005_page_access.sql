CREATE TABLE content_page_access (
 content_id TEXT PRIMARY KEY NOT NULL REFERENCES content(id) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN ('public','blocked','password')),
 message TEXT NOT NULL DEFAULT '',
 password_hash TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL CHECK(revision > 0),
 updated_at TEXT NOT NULL
);
