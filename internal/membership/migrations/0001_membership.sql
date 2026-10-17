CREATE TABLE membership_plans (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, limits_json TEXT NOT NULL CHECK(json_valid(limits_json))
);
CREATE TABLE membership_grants (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, owner_id TEXT NOT NULL REFERENCES users(id),
 plan_id TEXT NOT NULL REFERENCES membership_plans(id), status TEXT NOT NULL CHECK(status IN('active','pending-renewal','expired','cancelled','suspended')),
 period_start TEXT NOT NULL DEFAULT '', period_end TEXT NOT NULL DEFAULT '', grace_until TEXT NOT NULL DEFAULT '',
 cancel_at_period_end INTEGER NOT NULL DEFAULT 0 CHECK(cancel_at_period_end IN(0,1)),
 version TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX membership_owner ON membership_grants(owner_id);
CREATE TABLE membership_policies (
 content_type TEXT PRIMARY KEY, free_limit INTEGER NOT NULL CHECK(free_limit>=0),
 bypass_permission TEXT NOT NULL DEFAULT 'membership.unlimited'
);
INSERT OR IGNORE INTO role_permissions(role,permission) SELECT slug,'membership.read' FROM roles WHERE slug IN('customer','author','editor','contributor');

INSERT OR IGNORE INTO role_permissions(role,permission) SELECT slug,'membership.submit' FROM roles WHERE slug IN('customer','author','editor','contributor');
