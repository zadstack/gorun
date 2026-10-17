-- Widen the execution registry without rebuilding the agent table or touching
-- its referencing workflows/runs. Preserve every existing kind exactly.
ALTER TABLE workagents_agents RENAME COLUMN execution_kind TO previous_execution_kind;
ALTER TABLE workagents_agents ADD COLUMN execution_kind TEXT NOT NULL DEFAULT 'text' CHECK(execution_kind IN ('text','github_stars','content_draft','github_starred'));
UPDATE workagents_agents SET execution_kind=previous_execution_kind;
ALTER TABLE workagents_agents DROP COLUMN previous_execution_kind;
