CREATE TABLE workagents_linear_flows (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL REFERENCES users(id),
 request_key TEXT NOT NULL,
 plan TEXT NOT NULL CHECK(json_valid(plan)),
 step INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running','awaiting_approval','done','rejected','failed')),
 result TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 approved_step INTEGER NOT NULL DEFAULT -1,
 approved_by TEXT NOT NULL DEFAULT '',
 approved_hash TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(owner_id,request_key)
);
