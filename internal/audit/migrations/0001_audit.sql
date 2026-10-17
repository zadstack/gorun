CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    at          TEXT NOT NULL,
    actor_kind  TEXT NOT NULL CHECK (actor_kind IN ('user', 'api_key', 'system', 'extension')),
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target_kind TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    meta_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta_json))
);
CREATE INDEX audit_log_at ON audit_log (at);
CREATE INDEX audit_log_action ON audit_log (action);
CREATE INDEX audit_log_actor ON audit_log (actor_kind, actor_id);
CREATE INDEX audit_log_target ON audit_log (target_kind, target_id);
