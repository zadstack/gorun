-- status: queued | running | done | dead are written by the worker.
-- 'failed' is kept for spec fidelity but never stored: a failed attempt with
-- retries left returns to 'queued' with a future run_at (see queue.go).
CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,
    payload_json    TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(payload_json)),
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'running', 'done', 'failed', 'dead')),
    run_at          TEXT NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    last_error      TEXT NOT NULL DEFAULT '',
    locked_by       TEXT,
    locked_at       TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    idempotency_key TEXT UNIQUE
);
CREATE INDEX jobs_claim ON jobs (status, run_at);
CREATE INDEX jobs_kind ON jobs (kind);
