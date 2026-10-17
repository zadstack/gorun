-- current_at (a ULID, '' = never) marks a revision made current again after
-- a newer one: FindBySourceURL orders by it (else by id), newest first.
ALTER TABLE media ADD COLUMN current_at TEXT NOT NULL DEFAULT '';
