CREATE TABLE workagents_review_previews (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), page_id TEXT NOT NULL, agent_id TEXT NOT NULL,
 context TEXT NOT NULL, snapshot TEXT NOT NULL, destination TEXT NOT NULL, config_revision TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE workagents_design_reviews (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), preview_id TEXT NOT NULL REFERENCES workagents_review_previews(id),
 request_key TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'queued', error_code TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 1, findings TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(owner_id,request_key)
);
