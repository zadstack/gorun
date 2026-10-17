-- from_path is stored normalized (decoded, leading "/", no trailing "/"
-- except the root), so UNIQUE also covers trailing-slash variants.
CREATE TABLE redirects (
    id         TEXT PRIMARY KEY,
    from_path  TEXT NOT NULL UNIQUE,
    to_url     TEXT NOT NULL,
    status     INTEGER NOT NULL DEFAULT 301 CHECK (status IN (301, 302, 307, 308)),
    hits       INTEGER NOT NULL DEFAULT 0,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE INDEX redirects_created_at ON redirects (created_at);
