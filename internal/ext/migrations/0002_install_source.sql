-- Older records contain no proof of whether a ZIP upload or directory was used.
-- Record only the method of new installations, never package trust or authorship.
ALTER TABLE extensions ADD COLUMN install_source TEXT NOT NULL DEFAULT 'legacy_unknown'
  CHECK (install_source IN ('legacy_unknown', 'directory', 'upload'));
