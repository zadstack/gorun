-- Separate POC schema: never apply this to a GoRun Full SQLite database.
CREATE TABLE edge_schema (version INTEGER PRIMARY KEY CHECK (version = 1));
INSERT INTO edge_schema (version) VALUES (1);
CREATE TABLE edge_sites (id TEXT PRIMARY KEY, title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 200), default_locale TEXT NOT NULL CHECK(default_locale IN ('en','ar')));
CREATE TABLE edge_content (
 site_id TEXT NOT NULL REFERENCES edge_sites(id), id TEXT NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('post','page')),
 slug TEXT NOT NULL CHECK(length(slug) BETWEEN 1 AND 120),
 locale TEXT NOT NULL CHECK(locale IN ('en','ar')),
 title TEXT NOT NULL CHECK(length(title) <= 200),
 excerpt TEXT NOT NULL DEFAULT '' CHECK(length(excerpt) <= 600),
 body_text TEXT NOT NULL DEFAULT '' CHECK(length(body_text) <= 100000),
 status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','published','private')),
 published_at TEXT,
 PRIMARY KEY(site_id,id), UNIQUE(site_id,type,slug,locale),
 CHECK(status <> 'published' OR (published_at IS NOT NULL AND length(published_at)=20 AND published_at GLOB '????-??-??T??:??:??Z'))
);
CREATE INDEX edge_public_content ON edge_content(site_id,status,locale,type,published_at,id);
CREATE TABLE edge_media (
 site_id TEXT NOT NULL REFERENCES edge_sites(id), id TEXT NOT NULL,
 object_key TEXT NOT NULL,
 content_type TEXT NOT NULL,
 byte_size INTEGER NOT NULL CHECK(byte_size BETWEEN 0 AND 10485760),
 visibility TEXT NOT NULL DEFAULT 'private' CHECK(visibility IN ('public','private')),
 PRIMARY KEY(site_id,id)
);
