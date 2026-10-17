-- Key console: approximate use counter (flushed with last_used_at, at most
-- once a minute per key) and the key a rotated key replaced.
ALTER TABLE api_keys ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN rotated_from TEXT NOT NULL DEFAULT '';
