CREATE TABLE admin_canvas_templates (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, title TEXT NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 edges_json TEXT NOT NULL CHECK(json_valid(edges_json)), created_at TEXT NOT NULL
);
CREATE INDEX admin_canvas_templates_owner ON admin_canvas_templates(owner_id,created_at);
CREATE TABLE admin_canvas_drafts (
 board_id TEXT NOT NULL,
 card_id TEXT NOT NULL, content_id TEXT NOT NULL,
 owner_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 source_json TEXT NOT NULL CHECK(json_valid(source_json)), created_at TEXT NOT NULL,
 PRIMARY KEY(board_id,card_id)
);
