-- API key kind: 'api' keys call the REST API (/api); 'agent' keys are for AI
-- agents and work only on /mcp (agents must never use the REST API, e.g. to
-- approve their own sensitive calls).
ALTER TABLE api_keys ADD COLUMN kind TEXT NOT NULL DEFAULT 'api' CHECK (kind IN ('api', 'agent'));
