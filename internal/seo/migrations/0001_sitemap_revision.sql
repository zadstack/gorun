-- Mutation triggers advance this revision in the same transaction as the write.
CREATE TABLE seo_sitemap_revision (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 0
);
INSERT INTO seo_sitemap_revision (id) VALUES (1);
