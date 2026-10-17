CREATE TABLE admin_dashboard_pins (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 nav_id TEXT NOT NULL,
 position INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(user_id,nav_id)
);
CREATE INDEX admin_dashboard_pins_order ON admin_dashboard_pins(user_id,position,nav_id);
