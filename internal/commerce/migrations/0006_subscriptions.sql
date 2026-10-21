-- Subscription billing. One-time products stay billing_mode=payment.
-- prices_json maps ISO currency to {"price_id","amount_minor"} (USD and AED
-- are the admin defaults; any 3-letter code is stored).

ALTER TABLE products ADD COLUMN billing_mode TEXT NOT NULL DEFAULT 'payment'
    CHECK (billing_mode IN ('payment', 'subscription'));
ALTER TABLE products ADD COLUMN stripe_price_id TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN billing_interval TEXT NOT NULL DEFAULT ''
    CHECK (billing_interval IN ('', 'month', 'year'));
ALTER TABLE products ADD COLUMN trial_days INTEGER NOT NULL DEFAULT 0
    CHECK (trial_days BETWEEN 0 AND 730);
ALTER TABLE products ADD COLUMN prices_json TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(prices_json));

CREATE TABLE subscriptions (
    id                     TEXT PRIMARY KEY,
    customer_id            TEXT NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    order_id               TEXT NULL REFERENCES orders (id) ON DELETE SET NULL,
    account_id             TEXT NOT NULL DEFAULT '',
    site_id                TEXT NOT NULL DEFAULT '',
    plan                   TEXT NOT NULL DEFAULT '',
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    stripe_customer_id     TEXT NOT NULL DEFAULT '',
    price_id               TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL,
    current_period_end     TEXT NOT NULL DEFAULT '',
    cancel_at_period_end   INTEGER NOT NULL DEFAULT 0,
    trial_end              TEXT NOT NULL DEFAULT '',
    grace_until            TEXT NOT NULL DEFAULT '',
    access_suspended       INTEGER NOT NULL DEFAULT 0,
    currency               TEXT NOT NULL DEFAULT '',
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL
);

CREATE INDEX subscriptions_customer ON subscriptions (customer_id, status);
CREATE INDEX subscriptions_account ON subscriptions (account_id, plan);
CREATE INDEX subscriptions_grace ON subscriptions (status, grace_until);

CREATE TABLE invoices (
    id                 TEXT PRIMARY KEY,
    customer_id        TEXT NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    subscription_id    TEXT NOT NULL DEFAULT '',
    stripe_invoice_id  TEXT NOT NULL UNIQUE,
    hosted_invoice_url TEXT NOT NULL DEFAULT '',
    pdf_url            TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT '',
    amount_minor       INTEGER NOT NULL DEFAULT 0,
    currency           TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL
);

CREATE INDEX invoices_customer ON invoices (customer_id, created_at);
