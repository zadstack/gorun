-- Extensions (spec §3 ext). secret_enc is the HMAC signing secret,
-- AES-GCM encrypted under GORUN_SECRET_KEY (aad "ext:<slug>").
-- failures counts consecutive delivery failures (20 → status 'error').
CREATE TABLE extensions (
    slug           TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    version        TEXT NOT NULL,
    manifest_json  TEXT NOT NULL CHECK (json_valid(manifest_json)),
    status         TEXT NOT NULL DEFAULT 'installed' CHECK (status IN ('installed', 'active', 'disabled', 'error')),
    endpoint_url   TEXT NOT NULL DEFAULT '',
    secret_enc     TEXT NOT NULL DEFAULT '',
    api_key_id     TEXT NOT NULL DEFAULT '',
    failures       INTEGER NOT NULL DEFAULT 0,
    total_failures INTEGER NOT NULL DEFAULT 0,
    installed_at   TEXT NOT NULL,
    activated_at   TEXT NOT NULL DEFAULT '',
    last_error     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE extension_subscriptions (
    extension_slug TEXT NOT NULL REFERENCES extensions (slug) ON DELETE CASCADE,
    hook           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('action', 'filter', 'event')),
    timeout_ms     INTEGER NOT NULL,
    PRIMARY KEY (extension_slug, hook, kind)
);

-- One row per bridge attempt; pruned to the newest 200 per extension.
CREATE TABLE extension_deliveries (
    id             TEXT PRIMARY KEY,
    extension_slug TEXT NOT NULL REFERENCES extensions (slug) ON DELETE CASCADE,
    delivery_id    TEXT NOT NULL,
    hook           TEXT NOT NULL,
    kind           TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('ok', 'error', 'timeout')),
    http_status    INTEGER NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

CREATE INDEX extension_deliveries_slug ON extension_deliveries (extension_slug, id);
