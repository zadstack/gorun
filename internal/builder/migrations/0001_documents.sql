CREATE TABLE builder_documents (
 content_id TEXT PRIMARY KEY REFERENCES content(id) ON DELETE CASCADE,
 document_json TEXT NOT NULL CHECK (json_valid(document_json)),
 published_document_json TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(published_document_json)),
 base_updated_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
