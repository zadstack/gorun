-- When the session was last used, for the active-sessions list on My
-- account. Refreshed at most every five minutes; sessions from before this
-- migration show their creation time until their next request.
ALTER TABLE sessions ADD COLUMN last_seen_at TEXT NOT NULL DEFAULT '';
