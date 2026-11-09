CREATE TABLE workagents_channels (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 120),
    status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','closed')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_id, title)
);
CREATE INDEX workagents_channels_owner ON workagents_channels(owner_id, status);

CREATE TABLE workagents_channel_members (
    channel_id TEXT NOT NULL REFERENCES workagents_channels(id),
    member_kind TEXT NOT NULL CHECK(member_kind IN ('user','agent_key')),
    member_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    added_at TEXT NOT NULL,
    PRIMARY KEY(channel_id, member_kind, member_id)
);
-- agent_key member_id must reference an api_keys(id) row of the same
-- owner: that is enforced in code (channels.go), because a table-level
-- foreign key cannot express the same-owner constraint.

CREATE TABLE workagents_channel_messages (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES workagents_channels(id),
    author_kind TEXT NOT NULL CHECK(author_kind IN ('user','agent_key')),
    author_id TEXT NOT NULL,
    author_name TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL CHECK(length(body) BETWEEN 1 AND 8192),
    meta_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(meta_json)),
    created_at TEXT NOT NULL
);
CREATE INDEX workagents_channel_messages_thread ON workagents_channel_messages(channel_id, created_at);
