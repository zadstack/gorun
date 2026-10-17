CREATE TABLE content_types (
    slug            TEXT PRIMARY KEY,
    name_json       TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(name_json)),
    singular_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(singular_json)),
    plural_json     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(plural_json)),
    icon            TEXT NOT NULL DEFAULT '',
    public          INTEGER NOT NULL DEFAULT 1,
    has_archive     INTEGER NOT NULL DEFAULT 0,
    archive_slug    TEXT NOT NULL DEFAULT '',
    hierarchical    INTEGER NOT NULL DEFAULT 0,
    supports_json   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(supports_json)),
    fields_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(fields_json)),
    taxonomies_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(taxonomies_json)),
    templates_json  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(templates_json)),
    system          INTEGER NOT NULL DEFAULT 0,
    menu_position   INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE content (
    id                TEXT PRIMARY KEY,
    type              TEXT NOT NULL REFERENCES content_types (slug) ON DELETE RESTRICT,
    slug              TEXT NOT NULL,
    title             TEXT NOT NULL DEFAULT '',
    excerpt           TEXT NOT NULL DEFAULT '',
    body              TEXT NOT NULL DEFAULT '',
    body_format       TEXT NOT NULL DEFAULT 'html',
    fields_json       TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields_json)),
    author_id         TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft', 'pending', 'scheduled', 'published', 'private', 'trash')),
    parent_id         TEXT REFERENCES content (id) ON DELETE SET NULL,
    menu_order        INTEGER NOT NULL DEFAULT 0,
    template          TEXT NOT NULL DEFAULT '',
    featured_media_id TEXT,
    seo_json          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(seo_json)),
    locale            TEXT NOT NULL DEFAULT '',
    publish_at        TEXT,
    published_at      TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    deleted_at        TEXT,
    UNIQUE (type, slug, locale)
);
CREATE INDEX content_type_status ON content (type, status, created_at);
CREATE INDEX content_status_publish_at ON content (status, publish_at);
CREATE INDEX content_parent ON content (parent_id);
CREATE INDEX content_author ON content (author_id);

CREATE TABLE content_index (
    content_id TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    field      TEXT NOT NULL,
    text_value TEXT,
    num_value  REAL,
    date_value TEXT,
    PRIMARY KEY (content_id, field)
);
CREATE INDEX content_index_text ON content_index (field, text_value);
CREATE INDEX content_index_num ON content_index (field, num_value);
CREATE INDEX content_index_date ON content_index (field, date_value);

CREATE TABLE content_relations (
    from_id  TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    field    TEXT NOT NULL,
    to_id    TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (from_id, field, to_id)
);
CREATE INDEX content_relations_to ON content_relations (to_id, field);

CREATE TABLE revisions (
    id            TEXT PRIMARY KEY,
    content_id    TEXT NOT NULL REFERENCES content (id) ON DELETE CASCADE,
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
    author_id     TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);
CREATE INDEX revisions_content ON revisions (content_id, id);

INSERT INTO content_types (slug, name_json, singular_json, plural_json, icon, public, has_archive, archive_slug, hierarchical,
                           supports_json, fields_json, taxonomies_json, templates_json, system, menu_position, created_at, updated_at)
VALUES
    ('post', '{"en":"Posts","ar":"المقالات"}', '{"en":"Post","ar":"مقال"}', '{"en":"Posts","ar":"المقالات"}', 'file-text',
     1, 1, 'blog', 0,
     '["title","body","excerpt","featured_image","author","revisions","seo"]', '[]', '["category","tag"]', '[]',
     1, 10, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
    ('page', '{"en":"Pages","ar":"الصفحات"}', '{"en":"Page","ar":"صفحة"}', '{"en":"Pages","ar":"الصفحات"}', 'file',
     1, 0, '', 1,
     '["title","body","featured_image","author","revisions","seo","parent","menu_order"]', '[]', '[]', '[]',
     1, 20, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
