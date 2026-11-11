-- Read state of the derived admin notifications (internal/admin/
-- notifications.go). Notifications themselves are computed from module
-- tables on request; a row here only says that user_id has seen key.
CREATE TABLE notifications_read (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    read_at TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
);
CREATE INDEX notifications_read_at ON notifications_read (read_at);
