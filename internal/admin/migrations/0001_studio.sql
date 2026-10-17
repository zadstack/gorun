CREATE TABLE admin_studio_candidates (
 id TEXT PRIMARY KEY,
 content_id TEXT NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('queued','running','waiting','ready','failed','removed')),
 document TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(user_id,request_key)
);
CREATE INDEX admin_studio_owner ON admin_studio_candidates(content_id,user_id,created_at);
