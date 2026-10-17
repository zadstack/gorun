CREATE TABLE form_request_keys (
 request_key TEXT PRIMARY KEY,
 submission_id TEXT NOT NULL REFERENCES form_submissions(id) ON DELETE CASCADE
);
CREATE TABLE form_receipt_deliveries (
 submission_id TEXT PRIMARY KEY REFERENCES form_submissions(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'sending',
 last_error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 sent_at TEXT
);
CREATE TABLE form_event_deliveries (
 submission_id TEXT PRIMARY KEY REFERENCES form_submissions(id) ON DELETE CASCADE
);
