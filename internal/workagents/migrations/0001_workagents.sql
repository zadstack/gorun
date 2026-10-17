CREATE TABLE workagents_agents (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), source_id TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', system_prompt TEXT NOT NULL DEFAULT '',
 connection_id TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 unsupported_tools TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(unsupported_tools)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX workagents_agent_source ON workagents_agents(source_id) WHERE source_id <> '';
CREATE TABLE workagents_workflows (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), agent_id TEXT NOT NULL REFERENCES workagents_agents(id),
 source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, input TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)), interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK(interval_seconds=0 OR interval_seconds BETWEEN 60 AND 31536000),
 next_run_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX workagents_workflow_source ON workagents_workflows(source_id) WHERE source_id <> '';
CREATE TABLE workagents_runs (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), agent_id TEXT NOT NULL REFERENCES workagents_agents(id),
 workflow_id TEXT NOT NULL DEFAULT '', source_id TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '',
 request_key TEXT NOT NULL DEFAULT '', input TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'queued', error_code TEXT NOT NULL DEFAULT '',
 started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 historical INTEGER NOT NULL DEFAULT 0 CHECK(historical IN (0,1))
);
CREATE UNIQUE INDEX workagents_run_source ON workagents_runs(source_id) WHERE source_id <> '';
CREATE UNIQUE INDEX workagents_run_request ON workagents_runs(owner_id,request_key) WHERE request_key <> '';
CREATE INDEX workagents_owner_runs ON workagents_runs(owner_id,created_at);
CREATE TABLE workagents_artifacts (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), run_id TEXT NOT NULL DEFAULT '',
 source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '',
 historical INTEGER NOT NULL DEFAULT 0 CHECK(historical IN (0,1)), created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX workagents_artifact_source ON workagents_artifacts(source_id) WHERE source_id <> '';
CREATE UNIQUE INDEX workagents_artifact_run ON workagents_artifacts(run_id) WHERE run_id <> '' AND historical=0;
CREATE TABLE workagents_approvals (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), run_id TEXT NOT NULL DEFAULT '',
 source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, status TEXT NOT NULL,
 decision TEXT NOT NULL DEFAULT '', historical INTEGER NOT NULL DEFAULT 1 CHECK(historical IN (0,1)), created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX workagents_approval_source ON workagents_approvals(source_id) WHERE source_id <> '';
