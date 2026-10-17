-- A full refund that arrives before the checkout completion is remembered on
-- the order, so the late completion marks it refunded instead of paying it.
ALTER TABLE orders ADD COLUMN refunded_pending INTEGER NOT NULL DEFAULT 0;
