-- One counter that every committed settings write advances, whichever
-- connection or process made it. Store's memo compares it instead of
-- PRAGMA data_version, which is per connection and ignores the connection's
-- own commits.
CREATE TABLE settings_revision (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    n  INTEGER NOT NULL
);
INSERT INTO settings_revision (id, n) VALUES (1, 1);
CREATE TRIGGER settings_revision_insert AFTER INSERT ON settings BEGIN
    UPDATE settings_revision SET n = n + 1 WHERE id = 1;
END;
CREATE TRIGGER settings_revision_update AFTER UPDATE ON settings BEGIN
    UPDATE settings_revision SET n = n + 1 WHERE id = 1;
END;
CREATE TRIGGER settings_revision_delete AFTER DELETE ON settings BEGIN
    UPDATE settings_revision SET n = n + 1 WHERE id = 1;
END;
