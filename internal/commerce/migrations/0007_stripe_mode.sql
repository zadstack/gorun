-- Stripe test/live provenance. An order records the mode whose key created
-- its Checkout Session; a Stripe event records the mode whose webhook secret
-- verified it and whether it came from Stripe or from the local
-- "send test event" button. Rows from before this migration keep '' and are
-- never relabelled.
ALTER TABLE orders ADD COLUMN stripe_mode TEXT NOT NULL DEFAULT ''
    CHECK (stripe_mode IN ('', 'test', 'live'));
ALTER TABLE stripe_events ADD COLUMN mode TEXT NOT NULL DEFAULT ''
    CHECK (mode IN ('', 'test', 'live'));
ALTER TABLE stripe_events ADD COLUMN source TEXT NOT NULL DEFAULT ''
    CHECK (source IN ('', 'stripe', 'local'));
CREATE INDEX stripe_events_mode_source ON stripe_events (mode, source, received_at);
