ALTER TABLE products ADD COLUMN kind TEXT NOT NULL DEFAULT 'provisioning' CHECK(kind IN ('provisioning','service','digital'));
ALTER TABLE products ADD COLUMN digital_file TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN download_days INTEGER NOT NULL DEFAULT 7;
ALTER TABLE orders ADD COLUMN checkout_key TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD COLUMN checkout_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX orders_checkout_key ON orders(checkout_key) WHERE checkout_key <> '';
CREATE TABLE order_items (
 id TEXT PRIMARY KEY, order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
 product_id TEXT NOT NULL, quantity INTEGER NOT NULL CHECK(quantity BETWEEN 1 AND 99),
 unit_minor INTEGER NOT NULL CHECK(unit_minor>=0), snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json))
);
CREATE INDEX order_items_order ON order_items(order_id);
ALTER TABLE orders ADD COLUMN request_status TEXT NOT NULL DEFAULT 'pending' CHECK(request_status IN ('pending','accepted','in_progress','fulfilled','cancelled'));
ALTER TABLE orders ADD COLUMN fulfillment_note TEXT NOT NULL DEFAULT '';
