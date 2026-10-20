-- A restored AI configuration must not revive an older outbound consent.
CREATE TABLE admin_canvas_ai_config_revision (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL);
INSERT INTO admin_canvas_ai_config_revision VALUES(1,1);
CREATE TRIGGER canvas_ai_config_insert AFTER INSERT ON settings WHEN NEW.key LIKE 'ai.%' BEGIN
 UPDATE admin_canvas_ai_config_revision SET revision=revision+1 WHERE id=1;
END;
CREATE TRIGGER canvas_ai_config_update AFTER UPDATE ON settings WHEN OLD.key LIKE 'ai.%' OR NEW.key LIKE 'ai.%' BEGIN
 UPDATE admin_canvas_ai_config_revision SET revision=revision+1 WHERE id=1;
END;
CREATE TRIGGER canvas_ai_config_delete AFTER DELETE ON settings WHEN OLD.key LIKE 'ai.%' BEGIN
 UPDATE admin_canvas_ai_config_revision SET revision=revision+1 WHERE id=1;
END;
