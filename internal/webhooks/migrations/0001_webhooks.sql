-- Outgoing webhooks: an endpoint subscribed to site events, signed with a
-- per-endpoint secret (stored encrypted with the site key).
CREATE TABLE webhooks (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    url                  TEXT NOT NULL,
    events_json          TEXT NOT NULL DEFAULT '[]',
    secret_enc           TEXT NOT NULL,
    active               INTEGER NOT NULL DEFAULT 1,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_status          TEXT NOT NULL DEFAULT '',
    last_delivery_at     TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

-- One row per event sent to an endpoint (and per redelivery). payload is the
-- exact JSON body that is signed and sent.
CREATE TABLE webhook_deliveries (
    id            TEXT PRIMARY KEY,
    webhook_id    TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event         TEXT NOT NULL,
    payload       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'delivered', 'retrying', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    response_code INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    delivered_at  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX webhook_deliveries_hook ON webhook_deliveries (webhook_id, created_at);
CREATE INDEX webhook_deliveries_created ON webhook_deliveries (created_at);
