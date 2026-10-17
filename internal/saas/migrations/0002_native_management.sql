ALTER TABLE saas_apps ADD COLUMN mode TEXT NOT NULL DEFAULT 'subscription' CHECK(mode IN ('subscription','external'));
ALTER TABLE saas_apps ADD COLUMN version TEXT NOT NULL DEFAULT '';
CREATE TABLE saas_configuration_requests (
 actor_id TEXT NOT NULL REFERENCES users(id), kind TEXT NOT NULL, request_key TEXT NOT NULL,
 payload_hash TEXT NOT NULL, result_id TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(actor_id,kind,request_key)
);
INSERT OR IGNORE INTO role_permissions(role,permission) SELECT slug,'saas.read' FROM roles WHERE slug='customer';
INSERT OR IGNORE INTO role_permissions(role,permission) SELECT slug,'saas.use' FROM roles WHERE slug='customer';
