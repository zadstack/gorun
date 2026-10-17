CREATE TABLE mcp_approvals (
    id           TEXT PRIMARY KEY,
    api_key_id   TEXT NOT NULL REFERENCES api_keys (id) ON DELETE CASCADE,
    key_name     TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL DEFAULT '',
    requester    TEXT NOT NULL DEFAULT '',
    tool         TEXT NOT NULL,
    args_json    TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(args_json)),
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'failed')),
    requested_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    decided_at   TEXT NOT NULL DEFAULT '',
    decided_by   TEXT NOT NULL DEFAULT '',
    executed_at  TEXT NOT NULL DEFAULT '',
    result_json  TEXT NOT NULL DEFAULT '' CHECK (result_json = '' OR json_valid(result_json))
);
CREATE INDEX mcp_approvals_status ON mcp_approvals (status, requested_at);
CREATE INDEX mcp_approvals_key ON mcp_approvals (api_key_id);
