CREATE TABLE admin_dashboard_layout (
 user_id TEXT PRIMARY KEY NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(version > 0),
 order_json TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
