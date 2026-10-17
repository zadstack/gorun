CREATE TABLE menus (
    id         TEXT PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    location   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX menus_location ON menus (location);

-- parent_id is NULL for top-level items; items are always replaced as a
-- whole tree (SaveItems), so it needs no FK of its own. Item IDs are unique
-- per menu (composite key), so copying a tree between menus cannot collide.
CREATE TABLE menu_items (
    id          TEXT NOT NULL,
    menu_id     TEXT NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
    parent_id   TEXT,
    position    INTEGER NOT NULL DEFAULT 0,
    label       TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL DEFAULT 'custom' CHECK (kind IN ('custom', 'content', 'term', 'archive')),
    ref_id      TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    target      TEXT NOT NULL DEFAULT '',
    css_class   TEXT NOT NULL DEFAULT '',
    open_in_new INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (menu_id, id)
);

CREATE INDEX menu_items_menu ON menu_items (menu_id, parent_id, position);
