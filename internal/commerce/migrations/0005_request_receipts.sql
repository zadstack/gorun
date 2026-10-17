-- Only newly submitted manual requests enqueue receipts. Never backfill old orders.
CREATE TABLE commerce_request_receipts (
 order_id TEXT PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
 recipient TEXT NOT NULL,
 subject TEXT NOT NULL,
 html_body TEXT NOT NULL,
 text_body TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','uncertain')),
 last_error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 sent_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX commerce_request_receipts_pending ON commerce_request_receipts(status,created_at);
