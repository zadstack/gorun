CREATE TABLE builder_revisions (
 content_id TEXT NOT NULL REFERENCES content(id) ON DELETE CASCADE,
 id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('save','publish','restore','reset')),
 document_json TEXT NOT NULL CHECK (json_valid(document_json)),
 author_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 section_count INTEGER NOT NULL DEFAULT 0,
 summary TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (content_id, id)
);
CREATE INDEX builder_revisions_content_id_id ON builder_revisions(content_id, id DESC);
