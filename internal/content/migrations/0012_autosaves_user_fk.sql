-- content_autosaves.user_id gains REFERENCES users (id) ON DELETE CASCADE,
-- like the other per-user tables, so deleting a user also deletes their
-- unsaved editor text (0010 had no foreign key on user_id). SQLite cannot add
-- a constraint to an existing table, so it is rebuilt with the same name,
-- columns and index; only rows whose user and item still exist are copied
-- (autosaves of users deleted before this migration are dropped). The
-- migration runner applies this file once, inside one transaction.
CREATE TABLE content_autosaves_new (
    content_id      TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    payload_json    TEXT NOT NULL CHECK (json_valid(payload_json)),
    base_edit_token TEXT NOT NULL,
    base_updated_at TEXT NOT NULL DEFAULT '',
    saved_at        TEXT NOT NULL,
    PRIMARY KEY (content_id, user_id)
);
INSERT INTO content_autosaves_new (content_id, user_id, payload_json, base_edit_token, base_updated_at, saved_at)
SELECT a.content_id, a.user_id, a.payload_json, a.base_edit_token, a.base_updated_at, a.saved_at
FROM content_autosaves a
WHERE EXISTS (SELECT 1 FROM users u WHERE u.id = a.user_id)
  AND EXISTS (SELECT 1 FROM content c WHERE c.id = a.content_id);
DROP TABLE content_autosaves;
ALTER TABLE content_autosaves_new RENAME TO content_autosaves;
CREATE INDEX content_autosaves_user ON content_autosaves (user_id);
