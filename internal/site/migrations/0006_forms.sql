-- Site forms: every form other than the contact form (which keeps
-- form_submissions) is a forms row with an ordered JSON field schema, and
-- each submission is a form_entries row with a JSON {field: value} map.
-- source: builder (declared by a page's form section; page_id is that page),
-- captured (created by its first submission), contact (reserved).
-- open = 0 once the builder section that declared it is gone.
CREATE TABLE forms (
    id          TEXT PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL,
    source      TEXT NOT NULL CHECK (source IN ('builder', 'captured', 'contact')),
    page_id     TEXT NOT NULL DEFAULT '',
    -- Builder forms: the section's submit button label and success message.
    submit_label TEXT NOT NULL DEFAULT '',
    success     TEXT NOT NULL DEFAULT '',
    fields_json TEXT NOT NULL CHECK (json_valid(fields_json)),
    open        INTEGER NOT NULL DEFAULT 1 CHECK (open IN (0, 1)),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX forms_page ON forms (source, page_id);

-- The publish time of the last builder.forms.published event applied per
-- page: a queued event that is older (delivered out of order) is ignored.
CREATE TABLE form_page_publishes (
    page_id      TEXT PRIMARY KEY,
    published_at TEXT NOT NULL
);

CREATE TABLE form_entries (
    id              TEXT PRIMARY KEY,
    form_id         TEXT NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    data_json       TEXT NOT NULL CHECK (json_valid(data_json)),
    page            TEXT NOT NULL DEFAULT '',
    ip              TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'read', 'spam')),
    attachment_name TEXT NOT NULL DEFAULT '',
    attachment_type TEXT NOT NULL DEFAULT '',
    attachment_size INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);
CREATE INDEX form_entries_form_created ON form_entries (form_id, created_at);
CREATE INDEX form_entries_form_status ON form_entries (form_id, status, created_at);

-- Replay keys for form_entries (a double submit stores one entry), kept for
-- ten minutes.
CREATE TABLE form_entry_keys (
    request_key TEXT PRIMARY KEY,
    entry_id    TEXT NOT NULL REFERENCES form_entries(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL
);
CREATE INDEX form_entry_keys_created ON form_entry_keys (created_at);
