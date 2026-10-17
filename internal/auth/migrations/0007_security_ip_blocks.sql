CREATE TABLE auth_ip_blocks (
 ip TEXT PRIMARY KEY,
 expires_at TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 event_id TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX auth_ip_blocks_expiry ON auth_ip_blocks(expires_at);
