CREATE TABLE user_navigation_preferences (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hidden_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(hidden_json)),
    updated_at TEXT NOT NULL
);
