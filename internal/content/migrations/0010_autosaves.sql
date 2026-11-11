-- Per-user editor autosaves. An autosave never touches the item itself: it
-- holds the unsaved editor fields of one user for one item, with the edit
-- token (content.EditToken) of the version the user was editing.
CREATE TABLE content_autosaves (
    content_id      TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    payload_json    TEXT NOT NULL CHECK (json_valid(payload_json)),
    base_edit_token TEXT NOT NULL,
    base_updated_at TEXT NOT NULL DEFAULT '',
    saved_at        TEXT NOT NULL,
    PRIMARY KEY (content_id, user_id)
);
CREATE INDEX content_autosaves_user ON content_autosaves (user_id);
