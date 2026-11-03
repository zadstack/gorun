-- analytics.db schema (spec §3). This file runs against analytics.db, not
-- the main database: the analytics module applies it itself (OpenStore).

CREATE TABLE events (
    id           INTEGER PRIMARY KEY,
    ts           TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK(kind IN ('view','submit')),
    path         TEXT NOT NULL,
    visitor      TEXT NOT NULL DEFAULT '',
    ref_host     TEXT NOT NULL DEFAULT '',
    utm_source   TEXT NOT NULL DEFAULT '',
    utm_medium   TEXT NOT NULL DEFAULT '',
    utm_campaign TEXT NOT NULL DEFAULT '',
    browser      TEXT NOT NULL DEFAULT '',
    os           TEXT NOT NULL DEFAULT '',
    device       TEXT NOT NULL DEFAULT '',
    country      TEXT NOT NULL DEFAULT '',
    locale       TEXT NOT NULL DEFAULT '',
    form         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_ts ON events(ts);
CREATE INDEX events_path_ts ON events(path, ts);
-- Covering index for the range visitor count (visitorQuery): kind and ts
-- bound the scan, visitor is read from the index without touching rows.
CREATE INDEX events_kind_ts_visitor ON events(kind, ts, visitor);

-- dim is one of: total, page, ref, utm, browser, os, device, country, form.
CREATE TABLE daily (
    day      TEXT,
    dim      TEXT,
    key      TEXT,
    views    INTEGER,
    visitors INTEGER,
    submits  INTEGER,
    PRIMARY KEY(day, dim, key)
);

CREATE TABLE salts (
    day  TEXT PRIMARY KEY,
    salt BLOB NOT NULL
);
