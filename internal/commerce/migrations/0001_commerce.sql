-- Commerce: sell access to MCP servers (spec §3 commerce, §4.6).
-- Unset optional references are NULL (never ''), so FKs and UNIQUE hold.

CREATE TABLE billing_servers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    base_url      TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    secret_enc    TEXT NOT NULL DEFAULT '',
    protocol      TEXT NOT NULL DEFAULT 'mcbb_v1' CHECK (protocol IN ('mcbb_v1')),
    status        TEXT NOT NULL DEFAULT 'unknown',
    last_check_at TEXT NOT NULL DEFAULT '',
    last_error    TEXT NOT NULL DEFAULT '',
    catalog_json  TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(catalog_json)),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE products (
    id                TEXT PRIMARY KEY,
    slug              TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    short_description TEXT NOT NULL DEFAULT '',
    price_minor       INTEGER NOT NULL DEFAULT 0 CHECK (price_minor >= 0),
    currency          TEXT NOT NULL DEFAULT 'AED',
    active            INTEGER NOT NULL DEFAULT 0,
    group_slug        TEXT NOT NULL DEFAULT '',
    group_name        TEXT NOT NULL DEFAULT '',
    plan_code         TEXT NOT NULL DEFAULT '',
    server_id         TEXT NULL REFERENCES billing_servers (id) ON DELETE SET NULL,
    access_months     INTEGER NOT NULL DEFAULT 1 CHECK (access_months BETWEEN 1 AND 120),
    features_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(features_json)),
    image_media_id    TEXT NOT NULL DEFAULT '',
    sort              INTEGER NOT NULL DEFAULT 0,
    seo_json          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(seo_json)),
    wp_product_id     TEXT NULL UNIQUE,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX products_listing ON products (active, group_slug, sort);

-- Deleting a user keeps the customer (and its orders) with user_id NULL.
CREATE TABLE customers (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NULL UNIQUE REFERENCES users (id) ON DELETE SET NULL,
    stripe_customer_id TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL
);

-- product_id is not a foreign key: product_snapshot_json keeps the order
-- meaningful after the product is edited or deleted.
CREATE TABLE orders (
    id                    TEXT PRIMARY KEY,
    number                TEXT NOT NULL UNIQUE,
    customer_id           TEXT NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    email                 TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending', 'paid', 'provisioning', 'active', 'failed', 'refunded', 'cancelled')),
    currency              TEXT NOT NULL,
    total_minor           INTEGER NOT NULL,
    product_id            TEXT NOT NULL,
    product_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(product_snapshot_json)),
    stripe_session_id     TEXT NOT NULL DEFAULT '',
    stripe_payment_intent TEXT NOT NULL DEFAULT '',
    paid_at               TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);

CREATE INDEX orders_customer ON orders (customer_id, created_at);
CREATE INDEX orders_status ON orders (status, created_at);
CREATE INDEX orders_payment_intent ON orders (stripe_payment_intent);

-- Exactly one entitlement per order (UNIQUE order_id): a webhook replay or a
-- concurrent provision run can never create a second one.
CREATE TABLE entitlements (
    id           TEXT PRIMARY KEY,
    customer_id  TEXT NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    order_id     TEXT NOT NULL UNIQUE REFERENCES orders (id) ON DELETE RESTRICT,
    product_id   TEXT NOT NULL,
    server_id    TEXT NOT NULL,
    plan_code    TEXT NOT NULL,
    external_id  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'active', 'suspended', 'expired', 'failed')),
    starts_at    TEXT NOT NULL DEFAULT '',
    ends_at      TEXT NOT NULL DEFAULT '',
    last_sync_at TEXT NOT NULL DEFAULT '',
    meta_json    TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta_json)),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX entitlements_customer ON entitlements (customer_id, status);
CREATE INDEX entitlements_expiry ON entitlements (status, ends_at);

CREATE TABLE provisioning_ops (
    id              TEXT PRIMARY KEY,
    entitlement_id  TEXT NOT NULL REFERENCES entitlements (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('create', 'extend', 'suspend', 'resume', 'login_link', 'reconcile')),
    idempotency_key TEXT NOT NULL UNIQUE,
    request_json    TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(request_json)),
    response_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(response_json)),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX provisioning_ops_entitlement ON provisioning_ops (entitlement_id, created_at);

CREATE TABLE stripe_events (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    received_at  TEXT NOT NULL,
    processed_at TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json))
);
