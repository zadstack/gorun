CREATE TABLE diagnostic_events (
 day TEXT NOT NULL,
 category TEXT NOT NULL,
 code TEXT NOT NULL,
 http_status INTEGER NOT NULL DEFAULT 0,
 first_at TEXT NOT NULL,
 last_at TEXT NOT NULL,
 count INTEGER NOT NULL,
 PRIMARY KEY(day,category,code,http_status)
);
CREATE INDEX diagnostic_events_last ON diagnostic_events(last_at);
