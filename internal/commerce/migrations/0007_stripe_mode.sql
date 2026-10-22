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
-- config_rev is a keyed fingerprint of the mode's key and webhook secret at
-- verification time (never the secrets): delivery evidence counts only while
-- the same configuration is in place.
ALTER TABLE stripe_events ADD COLUMN config_rev TEXT NOT NULL DEFAULT '';
CREATE INDEX stripe_events_mode_source ON stripe_events (mode, source, config_rev, received_at);
