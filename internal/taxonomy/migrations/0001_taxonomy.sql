CREATE TABLE taxonomies (
    slug          TEXT PRIMARY KEY,
    name_json     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(name_json)),
    singular_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(singular_json)),
    hierarchical  INTEGER NOT NULL DEFAULT 0,
    types_json    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(types_json)),
    public        INTEGER NOT NULL DEFAULT 1,
    system        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE terms (
    id          TEXT PRIMARY KEY,
    taxonomy    TEXT NOT NULL REFERENCES taxonomies (slug) ON DELETE CASCADE,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    parent_id   TEXT REFERENCES terms (id) ON DELETE SET NULL,
    meta_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta_json)),
    count       INTEGER NOT NULL DEFAULT 0, -- kept for the spec; Count is computed live on read
    UNIQUE (taxonomy, slug)
);
CREATE INDEX terms_parent ON terms (taxonomy, parent_id);

CREATE TABLE content_terms (
    content_id TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    term_id    TEXT NOT NULL REFERENCES terms (id) ON DELETE CASCADE,
    PRIMARY KEY (content_id, term_id)
);
CREATE INDEX content_terms_term ON content_terms (term_id);

INSERT INTO taxonomies (slug, name_json, singular_json, hierarchical, types_json, public, system) VALUES
    ('category', '{"en":"Categories","ar":"التصنيفات"}', '{"en":"Category","ar":"تصنيف"}', 1, '["post"]', 1, 1),
    ('tag', '{"en":"Tags","ar":"الوسوم"}', '{"en":"Tag","ar":"وسم"}', 0, '["post"]', 1, 1);
