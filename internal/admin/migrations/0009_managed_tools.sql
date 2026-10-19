CREATE TABLE admin_agent_tool_catalogs (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, key_id TEXT NOT NULL,
 bundle_version TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)), expires_at TEXT NOT NULL
);
CREATE INDEX admin_agent_tool_catalog_expiry ON admin_agent_tool_catalogs(expires_at);
CREATE TABLE admin_canvas_tool_states (
 run_id TEXT PRIMARY KEY REFERENCES admin_canvas_runs(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 document TEXT NOT NULL CHECK(json_valid(document))
);

-- Monotonic authority history: restoring a revoked grant or role cannot revive
-- a previously reviewed tool action. Only tool-enabled preparations bind this.
CREATE TABLE admin_canvas_tool_authority (
 board_id TEXT PRIMARY KEY REFERENCES admin_canvases(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL DEFAULT 1
);
INSERT INTO admin_canvas_tool_authority(board_id,owner_id) SELECT id,owner_id FROM admin_canvases;
CREATE TRIGGER canvas_tool_board_insert AFTER INSERT ON admin_canvases BEGIN
 INSERT INTO admin_canvas_tool_authority(board_id,owner_id) VALUES(NEW.id,NEW.owner_id);
END;
CREATE TRIGGER canvas_tool_board_owner AFTER UPDATE OF owner_id ON admin_canvases BEGIN
 UPDATE admin_canvas_tool_authority SET owner_id=NEW.owner_id,epoch=epoch+1 WHERE board_id=NEW.id;
END;
CREATE TRIGGER canvas_tool_grant_insert AFTER INSERT ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id=NEW.board_id;
END;
CREATE TRIGGER canvas_tool_role_insert AFTER INSERT ON role_permissions BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id IN(SELECT id FROM users WHERE role=NEW.role);
END;
CREATE TRIGGER canvas_tool_setting_insert AFTER INSERT ON settings WHEN NEW.key LIKE 'agents.bundle.%' OR NEW.key LIKE 'agents.model.%' OR NEW.key LIKE 'connections.profile.%' OR NEW.key='site.workspace' BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1;
END;
CREATE TRIGGER canvas_tool_grant_update AFTER UPDATE ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id IN(OLD.board_id,NEW.board_id);
END;
CREATE TRIGGER canvas_tool_role_update AFTER UPDATE ON role_permissions BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id IN(SELECT id FROM users WHERE role IN(OLD.role,NEW.role));
END;
CREATE TRIGGER canvas_tool_setting_update AFTER UPDATE ON settings WHEN OLD.key LIKE 'agents.bundle.%' OR OLD.key LIKE 'agents.model.%' OR OLD.key LIKE 'connections.profile.%' OR OLD.key='site.workspace' OR NEW.key LIKE 'agents.bundle.%' OR NEW.key LIKE 'agents.model.%' OR NEW.key LIKE 'connections.profile.%' OR NEW.key='site.workspace' BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1;
END;
CREATE TRIGGER canvas_tool_grant_delete AFTER DELETE ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id=OLD.board_id;
END;
CREATE TRIGGER canvas_tool_role_delete AFTER DELETE ON role_permissions BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id IN(SELECT id FROM users WHERE role=OLD.role);
END;
CREATE TRIGGER canvas_tool_setting_delete AFTER DELETE ON settings WHEN OLD.key LIKE 'agents.bundle.%' OR OLD.key LIKE 'agents.model.%' OR OLD.key LIKE 'connections.profile.%' OR OLD.key='site.workspace' BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1;
END;
CREATE TRIGGER canvas_tool_key_update AFTER UPDATE OF user_id,scopes_json,revoked_at,expires_at,kind ON api_keys BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id IN(OLD.user_id,NEW.user_id);
END;
CREATE TRIGGER canvas_tool_key_delete AFTER DELETE ON api_keys BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id=OLD.user_id;
END;
CREATE TRIGGER canvas_tool_owner_update AFTER UPDATE OF role,status ON users BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE owner_id=NEW.id;
END;
