CREATE TABLE saas_fleets (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, public_id TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE saas_apps (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, public_id TEXT NOT NULL UNIQUE,
 owner_id TEXT REFERENCES users(id), fleet_id TEXT REFERENCES saas_fleets(id), name TEXT NOT NULL,
 slug TEXT NOT NULL UNIQUE, base_url TEXT NOT NULL DEFAULT '', runtime TEXT NOT NULL DEFAULT '', framework TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL, manifest_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(manifest_json)),
 last_seen_at TEXT NOT NULL DEFAULT '', fulfillment_enabled INTEGER NOT NULL DEFAULT 0 CHECK(fulfillment_enabled IN(0,1)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE saas_entitlements (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, public_id TEXT NOT NULL UNIQUE,
 app_id TEXT NOT NULL REFERENCES saas_apps(id), owner_id TEXT REFERENCES users(id), subject_type TEXT NOT NULL,
 external_id TEXT NOT NULL, plan_id TEXT NOT NULL, status TEXT NOT NULL,
 features_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(features_json)), limits_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(limits_json)),
 valid_until TEXT NOT NULL DEFAULT '', source_order_id TEXT NOT NULL DEFAULT '', source_item_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX saas_entitlements_owner ON saas_entitlements(owner_id, app_id);
CREATE TABLE saas_usage_counters (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, app_id TEXT NOT NULL REFERENCES saas_apps(id), subject_id TEXT NOT NULL,
 metric TEXT NOT NULL, quantity INTEGER NOT NULL DEFAULT 0 CHECK(quantity>=0), created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(app_id,subject_id,metric)
);
CREATE TABLE saas_key_versions (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, public_id TEXT NOT NULL UNIQUE, app_id TEXT NOT NULL REFERENCES saas_apps(id), status TEXT NOT NULL,
 secret_ciphertext TEXT NOT NULL DEFAULT '', secret_iv TEXT NOT NULL DEFAULT '', secret_tag TEXT NOT NULL DEFAULT '', kdf_context TEXT NOT NULL DEFAULT '',
 protocol TEXT NOT NULL DEFAULT 'legacy-unconfigured', created_at TEXT NOT NULL, activated_at TEXT NOT NULL DEFAULT '',
 grace_started_at TEXT NOT NULL DEFAULT '', grace_expires_at TEXT NOT NULL DEFAULT '', revoked_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL
);
CREATE TABLE saas_consumed_nonces (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, nonce TEXT NOT NULL, api_key_public_id TEXT NOT NULL, consumed_at TEXT NOT NULL, expires_at TEXT NOT NULL,
 UNIQUE(api_key_public_id,nonce)
);
CREATE TABLE saas_webhook_ledger (
 id TEXT PRIMARY KEY, source_id TEXT UNIQUE, event_id TEXT NOT NULL, app_id TEXT NOT NULL REFERENCES saas_apps(id),
 idempotency_key TEXT NOT NULL, payload_hash TEXT NOT NULL, received_at TEXT NOT NULL,
 UNIQUE(app_id,idempotency_key), UNIQUE(app_id,event_id)
);
CREATE TABLE saas_usage_events (
 id TEXT PRIMARY KEY, entitlement_id TEXT NOT NULL REFERENCES saas_entitlements(id), idempotency_key TEXT NOT NULL,
 payload_hash TEXT NOT NULL, quantity INTEGER NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(entitlement_id,idempotency_key)
);
