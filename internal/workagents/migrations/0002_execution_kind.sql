ALTER TABLE workagents_agents ADD COLUMN execution_kind TEXT NOT NULL DEFAULT 'text' CHECK(execution_kind IN ('text','github_stars','content_draft'));
