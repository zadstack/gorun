-- WordPress migrator (spec §3 wpmigrate). Extra columns beyond the spec:
-- wp_sources.kind/file_path (WXR uploads), migrations.current_step /
-- current_phase / run / run_fetch / cursor / created_by (job pipeline state),
-- migration_items.phase / payload_json / would_json / meta_json /
-- local_url / updated_at (item payload, dry-run plan, provenance).
CREATE TABLE wp_sources (
	id                  TEXT PRIMARY KEY,
	name                TEXT NOT NULL DEFAULT '',
	kind                TEXT NOT NULL DEFAULT 'rest' CHECK (kind IN ('rest', 'wxr')),
	url                 TEXT NOT NULL DEFAULT '',
	username            TEXT NOT NULL DEFAULT '',
	app_password_enc    TEXT NOT NULL DEFAULT '',
	file_path           TEXT NOT NULL DEFAULT '',
	created_at          TEXT NOT NULL,
	last_discovery_json TEXT CHECK (last_discovery_json IS NULL OR json_valid(last_discovery_json)),
	last_discovery_at   TEXT
);

CREATE TABLE migrations (
	id             TEXT PRIMARY KEY,
	source_id      TEXT NOT NULL REFERENCES wp_sources(id) ON DELETE CASCADE,
	status         TEXT NOT NULL DEFAULT 'draft'
		CHECK (status IN ('draft', 'scanned', 'mapped', 'previewed', 'running', 'paused', 'completed', 'failed', 'validated')),
	options_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options_json)),
	mapping_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(mapping_json)),
	discovery_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(discovery_json)),
	stats_json     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(stats_json)),
	dry_run        INTEGER NOT NULL DEFAULT 0,
	current_step   TEXT NOT NULL DEFAULT '',
	current_phase  INTEGER NOT NULL DEFAULT 0,
	run            INTEGER NOT NULL DEFAULT 0,
	run_fetch      INTEGER NOT NULL DEFAULT 0,
	cursor         INTEGER NOT NULL DEFAULT 0,
	created_by     TEXT NOT NULL DEFAULT '',
	started_at     TEXT,
	finished_at    TEXT,
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);
CREATE INDEX migrations_source ON migrations(source_id);

CREATE TABLE migration_items (
	id           TEXT PRIMARY KEY,
	migration_id TEXT NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
	kind         TEXT NOT NULL CHECK (kind IN ('user', 'term', 'media', 'content', 'product', 'menu', 'redirect', 'rewrite')),
	wp_type      TEXT NOT NULL DEFAULT '',
	wp_id        TEXT NOT NULL,
	wp_slug      TEXT NOT NULL DEFAULT '',
	wp_url       TEXT NOT NULL DEFAULT '',
	local_kind   TEXT NOT NULL DEFAULT '',
	local_id     TEXT NOT NULL DEFAULT '',
	local_url    TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed', 'skipped', 'preview')),
	phase        INTEGER NOT NULL DEFAULT 0,
	error        TEXT NOT NULL DEFAULT '',
	attempts     INTEGER NOT NULL DEFAULT 0,
	checksum     TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
	would_json   TEXT CHECK (would_json IS NULL OR json_valid(would_json)),
	meta_json    TEXT CHECK (meta_json IS NULL OR json_valid(meta_json)),
	updated_at   TEXT NOT NULL,
	UNIQUE (migration_id, kind, wp_type, wp_id)
);
CREATE INDEX migration_items_queue ON migration_items(migration_id, kind, phase, status);
CREATE INDEX migration_items_wp ON migration_items(kind, wp_type, wp_id);

CREATE TABLE migration_log (
	id           TEXT PRIMARY KEY,
	migration_id TEXT NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
	at           TEXT NOT NULL,
	level        TEXT NOT NULL DEFAULT 'info' CHECK (level IN ('debug', 'info', 'warn', 'error')),
	item_id      TEXT,
	message      TEXT NOT NULL,
	meta_json    TEXT CHECK (meta_json IS NULL OR json_valid(meta_json))
);
CREATE INDEX migration_log_migration ON migration_log(migration_id, at);
