-- author_id is a plain reference (no FK): media survives user deletion.
-- source_url is NULL for uploads; a partial UNIQUE index makes FromURL
-- dedupe race-free.
CREATE TABLE media (
    id            TEXT PRIMARY KEY,
    filename      TEXT NOT NULL,
    storage_key   TEXT NOT NULL UNIQUE,
    url           TEXT NOT NULL,
    mime          TEXT NOT NULL,
    size          INTEGER NOT NULL DEFAULT 0,
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    alt           TEXT NOT NULL DEFAULT '',
    caption       TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    author_id     TEXT NOT NULL DEFAULT '',
    variants_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(variants_json)),
    source_url    TEXT,
    checksum      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

CREATE UNIQUE INDEX media_source_url ON media (source_url) WHERE source_url IS NOT NULL;
CREATE INDEX media_created_at ON media (created_at);
CREATE INDEX media_checksum ON media (checksum);
