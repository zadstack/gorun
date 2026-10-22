-- One row per observed capability key. The saved success (success_*,
-- result_json) is kept separately from the latest attempt so a failed or
-- superseded refresh never erases or relabels the last good read. Rows are
-- fenced by generation (one per admitted refresh) and by the configuration
-- revision the attempt was queued for.
CREATE TABLE observations (
    kind                TEXT NOT NULL,
    key                 TEXT NOT NULL,
    generation          INTEGER NOT NULL DEFAULT 0,
    job_id              TEXT NOT NULL DEFAULT '',
    pending_revision    TEXT NOT NULL DEFAULT '',
    attempt_status      TEXT NOT NULL DEFAULT ''
                        CHECK (attempt_status IN ('', 'waiting', 'running', 'succeeded', 'failed', 'superseded')),
    attempt_started_at  TEXT NOT NULL DEFAULT '',
    attempt_finished_at TEXT NOT NULL DEFAULT '',
    attempt_number      INTEGER NOT NULL DEFAULT 0,
    attempt_error       TEXT NOT NULL DEFAULT '',
    attempt_remediation TEXT NOT NULL DEFAULT '',
    attempt_duration_ms INTEGER,
    success_at          TEXT NOT NULL DEFAULT '',
    success_revision    TEXT NOT NULL DEFAULT '',
    success_duration_ms INTEGER,
    success_scope       TEXT NOT NULL DEFAULT '',
    result_json         TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(result_json)),
    updated_at          TEXT NOT NULL,
    PRIMARY KEY (kind, key)
);
