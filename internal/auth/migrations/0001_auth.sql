CREATE TABLE roles (
    slug      TEXT PRIMARY KEY,
    name_json TEXT NOT NULL CHECK (json_valid(name_json)),
    system    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE role_permissions (
    role       TEXT NOT NULL REFERENCES roles (slug) ON UPDATE CASCADE ON DELETE CASCADE,
    permission TEXT NOT NULL,
    PRIMARY KEY (role, permission)
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL REFERENCES roles (slug) ON UPDATE CASCADE ON DELETE RESTRICT,
    locale        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    last_login_at TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

-- id is the SHA-256 (hex) of the cookie token, so a DB leak exposes no sessions.
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    ua         TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- hash = SHA-256 (hex) of the full plaintext key "gp_<prefix><secret>".
CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL UNIQUE,
    hash         TEXT NOT NULL,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scopes_json  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(scopes_json)),
    rate_per_min INTEGER NOT NULL DEFAULT 60,
    expires_at   TEXT NOT NULL DEFAULT '',
    last_used_at TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    revoked_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX api_keys_user ON api_keys (user_id);

CREATE TABLE login_attempts (
    key          TEXT PRIMARY KEY,
    count        INTEGER NOT NULL,
    window_start TEXT NOT NULL
);

CREATE TABLE magic_links (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX magic_links_user ON magic_links (user_id);

INSERT INTO roles (slug, name_json, system) VALUES
    ('admin',       '{"en":"Administrator","ar":"مدير"}', 1),
    ('editor',      '{"en":"Editor","ar":"محرر"}', 1),
    ('author',      '{"en":"Author","ar":"كاتب"}', 1),
    ('contributor', '{"en":"Contributor","ar":"مساهم"}', 1),
    ('customer',    '{"en":"Customer","ar":"عميل"}', 1);

-- '*' grants every permission, including ones declared by later modules.
INSERT INTO role_permissions (role, permission) VALUES
    ('admin', '*'),
    ('editor', 'content.view'), ('editor', 'content.create'), ('editor', 'content.edit'),
    ('editor', 'content.publish'), ('editor', 'content.delete'),
    ('editor', 'media.upload'), ('editor', 'media.delete'),
    ('editor', 'taxonomy.manage'), ('editor', 'menus.manage'),
    ('author', 'content.view'), ('author', 'content.create'), ('author', 'content.edit'),
    ('author', 'content.publish'), ('author', 'media.upload'),
    ('contributor', 'content.view'), ('contributor', 'content.create'), ('contributor', 'content.edit');
