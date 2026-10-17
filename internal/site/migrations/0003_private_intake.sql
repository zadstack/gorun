CREATE TABLE private_intake (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK(kind IN ('incident','follow-up')),
    status TEXT NOT NULL DEFAULT 'new' CHECK(status IN ('new','reviewing','needs_information','closed')),
    payload TEXT NOT NULL,
    version TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX private_intake_status ON private_intake(status, created_at);
