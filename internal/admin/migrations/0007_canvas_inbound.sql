CREATE TABLE admin_canvas_inbound (
 id TEXT PRIMARY KEY,
 board_id TEXT NOT NULL REFERENCES admin_canvases(id) ON DELETE CASCADE,
 node_id TEXT NOT NULL, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 key_id TEXT NOT NULL DEFAULT '', command TEXT NOT NULL,
 secret TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 enabled INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'disabled',
 consent_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 UNIQUE(board_id,node_id)
);
CREATE TABLE admin_canvas_inbound_deliveries (
 endpoint_id TEXT NOT NULL REFERENCES admin_canvas_inbound(id) ON DELETE CASCADE,
 delivery_id TEXT NOT NULL, body_hash TEXT NOT NULL, run_id TEXT NOT NULL,
 created_at TEXT NOT NULL, PRIMARY KEY(endpoint_id,delivery_id)
);
CREATE INDEX admin_canvas_inbound_delivery_time ON admin_canvas_inbound_deliveries(created_at);
-- A monotonic revision prevents revoke-and-restore from reviving old consent.
CREATE TRIGGER canvas_inbound_grant_update AFTER UPDATE ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE board_id=NEW.board_id AND key_id=NEW.key_id;
END;
CREATE TRIGGER canvas_inbound_grant_delete AFTER DELETE ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE board_id=OLD.board_id AND key_id=OLD.key_id;
END;
CREATE TRIGGER canvas_inbound_grant_insert AFTER INSERT ON admin_canvas_grants BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE board_id=NEW.board_id AND key_id=NEW.key_id;
END;
CREATE TRIGGER canvas_inbound_key_update AFTER UPDATE OF user_id,scopes_json,revoked_at,expires_at,kind ON api_keys BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE key_id=NEW.id;
END;
CREATE TRIGGER canvas_inbound_key_delete AFTER DELETE ON api_keys BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE key_id=OLD.id;
END;
CREATE TRIGGER canvas_inbound_owner_update AFTER UPDATE OF role,status ON users BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE owner_id=NEW.id;
END;
CREATE TRIGGER canvas_inbound_role_insert AFTER INSERT ON role_permissions BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE owner_id IN(SELECT id FROM users WHERE role=NEW.role);
END;
CREATE TRIGGER canvas_inbound_role_delete AFTER DELETE ON role_permissions BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE owner_id IN(SELECT id FROM users WHERE role=OLD.role);
END;
CREATE TRIGGER canvas_inbound_role_update AFTER UPDATE ON role_permissions BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE owner_id IN(SELECT id FROM users WHERE role IN(OLD.role,NEW.role));
END;
CREATE TRIGGER canvas_inbound_settings_update AFTER UPDATE ON settings WHEN NEW.key LIKE 'ai.%' OR NEW.key LIKE 'agents.model.%' OR NEW.key LIKE 'connections.profile.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1;
END;
CREATE TRIGGER canvas_inbound_settings_insert AFTER INSERT ON settings WHEN NEW.key LIKE 'ai.%' OR NEW.key LIKE 'agents.model.%' OR NEW.key LIKE 'connections.profile.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1;
END;
CREATE TRIGGER canvas_inbound_settings_delete AFTER DELETE ON settings WHEN OLD.key LIKE 'ai.%' OR OLD.key LIKE 'agents.model.%' OR OLD.key LIKE 'connections.profile.%' BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1;
END;
CREATE TRIGGER canvas_inbound_graph_update AFTER UPDATE OF owner_id,document_json,edges_json ON admin_canvases BEGIN
 UPDATE admin_canvas_inbound SET enabled=0,status='needs_review',revision=revision+1 WHERE board_id=NEW.id AND (
 OLD.owner_id<>NEW.owner_id OR OLD.edges_json<>NEW.edges_json OR EXISTS(
 SELECT 1 FROM json_each(OLD.document_json) oldcard LEFT JOIN json_each(NEW.document_json) newcard
 ON json_extract(oldcard.value,'$.id')=json_extract(newcard.value,'$.id')
 WHERE (json_extract(oldcard.value,'$.id')=admin_canvas_inbound.node_id OR json_extract(oldcard.value,'$.id') IN(
 SELECT json_extract(edge.value,'$.from') FROM json_each(OLD.edges_json) edge WHERE json_extract(edge.value,'$.to')=admin_canvas_inbound.node_id))
 AND (newcard.value IS NULL OR json_remove(oldcard.value,'$.x','$.y')<>json_remove(newcard.value,'$.x','$.y'))));
END;
