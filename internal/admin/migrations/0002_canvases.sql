CREATE TABLE admin_canvases (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 title TEXT NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX admin_canvases_owner ON admin_canvases(owner_id,updated_at DESC,id);
