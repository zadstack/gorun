-- Staged update lifecycle. An operation is a durable state machine whose
-- phases are reported independently; a restarted process reconciles from
-- these rows instead of assuming success. ops_leases is the single durable
-- lock for update, rollback, backup and restore work on one data directory.
CREATE TABLE update_operations (
    id                  TEXT PRIMARY KEY,
    kind                TEXT NOT NULL DEFAULT 'update' CHECK (kind IN ('update')),
    status              TEXT NOT NULL
                        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'interrupted', 'rolled_back', 'rollback_failed')),
    current_phase       TEXT NOT NULL DEFAULT '',
    requested_version   TEXT NOT NULL,
    expected_digest     TEXT NOT NULL DEFAULT '',
    requested_by        TEXT NOT NULL DEFAULT '',
    running_version     TEXT NOT NULL DEFAULT '',
    activated_version   TEXT NOT NULL DEFAULT '',
    accepted_version    TEXT NOT NULL DEFAULT '',
    artifact_json       TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(artifact_json)),
    previous_json       TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(previous_json)),
    backup_stamp        TEXT NOT NULL DEFAULT '',
    deployment_ref      TEXT NOT NULL DEFAULT '',
    rollback_ref        TEXT NOT NULL DEFAULT '',
    migration_state     TEXT NOT NULL DEFAULT 'unchecked',
    source              TEXT NOT NULL DEFAULT '',
    adapter             TEXT NOT NULL DEFAULT '',
    run_token           TEXT NOT NULL DEFAULT '',
    heartbeat_at        TEXT NOT NULL DEFAULT '',
    error               TEXT NOT NULL DEFAULT '',
    remediation         TEXT NOT NULL DEFAULT '',
    retryable           INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    started_at          TEXT NOT NULL DEFAULT '',
    finished_at         TEXT NOT NULL DEFAULT '',
    updated_at          TEXT NOT NULL
);
CREATE INDEX update_operations_status ON update_operations (status, created_at);

CREATE TABLE update_phases (
    operation_id    TEXT NOT NULL REFERENCES update_operations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'passed', 'failed', 'skipped', 'interrupted')),
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER,
    progress        REAL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    result_json     TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(result_json)),
    error           TEXT NOT NULL DEFAULT '',
    remediation     TEXT NOT NULL DEFAULT '',
    retryable       INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (operation_id, name)
);

CREATE TABLE ops_leases (
    scope           TEXT PRIMARY KEY,
    holder          TEXT NOT NULL,
    owner           TEXT NOT NULL,
    operation_id    TEXT NOT NULL DEFAULT '',
    expires_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
