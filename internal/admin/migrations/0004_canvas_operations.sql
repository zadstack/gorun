ALTER TABLE admin_canvases ADD COLUMN edges_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(edges_json));
CREATE TABLE admin_canvas_reviews (
 id TEXT PRIMARY KEY, board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL, node_id TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)), expires_at TEXT NOT NULL
);
CREATE TABLE admin_canvas_runs (
 id TEXT PRIMARY KEY, board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL, key_id TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL,
 node_id TEXT NOT NULL, status TEXT NOT NULL, document TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(document)),
 accepted_run_id TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '', result_card_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '',
 UNIQUE(board_id,request_id)
);
CREATE TABLE admin_canvas_schedules (
 board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE, node_id TEXT NOT NULL,
 provider_node_id TEXT NOT NULL, interval TEXT NOT NULL, consent_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0, next_run_at TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'disabled',
 PRIMARY KEY(board_id,node_id)
);
CREATE TABLE admin_canvas_grants (
 board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE, key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
 can_read INTEGER NOT NULL DEFAULT 0, can_append INTEGER NOT NULL DEFAULT 0, can_run INTEGER NOT NULL DEFAULT 0,
 node_ids TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(node_ids)), PRIMARY KEY(board_id,key_id)
);
CREATE TABLE admin_canvas_provenance (
 board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE, card_id TEXT NOT NULL,
 actor_kind TEXT NOT NULL, actor_name TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL,
 PRIMARY KEY(board_id,card_id)
);
