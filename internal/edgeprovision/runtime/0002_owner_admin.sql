-- Additive standalone Edge mini-admin; never applied to GoRun Full.
ALTER TABLE edge_content ADD COLUMN admin_operation TEXT NOT NULL DEFAULT '';
ALTER TABLE edge_content ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0);
CREATE TABLE edge_owners(site_id TEXT PRIMARY KEY REFERENCES edge_sites(id), key_hash TEXT NOT NULL CHECK(length(key_hash)=64), auth_epoch INTEGER NOT NULL DEFAULT 1 CHECK(auth_epoch>0), active INTEGER NOT NULL DEFAULT 1 CHECK(active IN(0,1)));
CREATE TABLE edge_sessions(site_id TEXT NOT NULL REFERENCES edge_sites(id), token_hash TEXT NOT NULL, csrf TEXT NOT NULL, op_token TEXT NOT NULL DEFAULT '', owner_epoch INTEGER NOT NULL, issued_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(site_id,token_hash));
CREATE TABLE edge_login_used(site_id TEXT NOT NULL, nonce TEXT NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(site_id,nonce));
CREATE TABLE edge_login_limits(site_id TEXT PRIMARY KEY, window INTEGER NOT NULL, attempts INTEGER NOT NULL CHECK(attempts BETWEEN 0 AND 1000));
CREATE TABLE edge_drafts(site_id TEXT NOT NULL REFERENCES edge_sites(id), id TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0), source_version INTEGER NOT NULL CHECK(source_version>=0), body_json TEXT NOT NULL CHECK(json_valid(body_json)), op_token TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(site_id,id));
CREATE TABLE edge_revisions(site_id TEXT NOT NULL, id TEXT PRIMARY KEY, content_id TEXT NOT NULL, action TEXT NOT NULL, snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)), at TEXT NOT NULL);
CREATE INDEX edge_revisions_content ON edge_revisions(site_id,content_id,at);
CREATE TABLE edge_audit(site_id TEXT NOT NULL, id TEXT PRIMARY KEY, action TEXT NOT NULL, content_id TEXT NOT NULL DEFAULT '', at TEXT NOT NULL);
CREATE INDEX edge_audit_site ON edge_audit(site_id,at);
-- Offline owner changes atomically revoke sessions and append an auth audit.
CREATE TRIGGER edge_owner_epoch BEFORE UPDATE OF key_hash,auth_epoch,active ON edge_owners WHEN NEW.auth_epoch<=OLD.auth_epoch BEGIN SELECT RAISE(ABORT,'owner epoch must increase'); END;
CREATE TRIGGER edge_owner_created AFTER INSERT ON edge_owners BEGIN INSERT INTO edge_audit VALUES(NEW.site_id,lower(hex(randomblob(16))),'owner.created','',strftime('%Y-%m-%dT%H:%M:%fZ','now')); END;
CREATE TRIGGER edge_owner_recovered AFTER UPDATE OF key_hash,auth_epoch,active ON edge_owners BEGIN DELETE FROM edge_sessions WHERE site_id=NEW.site_id; DELETE FROM edge_login_used WHERE site_id=NEW.site_id; INSERT INTO edge_audit VALUES(NEW.site_id,lower(hex(randomblob(16))),CASE WHEN NEW.key_hash<>OLD.key_hash THEN 'owner.recovered' ELSE 'owner.sessions_revoked' END,'',strftime('%Y-%m-%dT%H:%M:%fZ','now')); END;
CREATE TRIGGER edge_owner_removed AFTER DELETE ON edge_owners BEGIN DELETE FROM edge_sessions WHERE site_id=OLD.site_id; DELETE FROM edge_login_used WHERE site_id=OLD.site_id; INSERT INTO edge_audit VALUES(OLD.site_id,lower(hex(randomblob(16))),'owner.removed','',strftime('%Y-%m-%dT%H:%M:%fZ','now')); END;
