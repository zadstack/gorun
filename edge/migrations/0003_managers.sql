-- Primary manager credential for GoRun Full. The raw key is never stored.
CREATE TABLE edge_managers(
  site_id TEXT NOT NULL REFERENCES edge_sites(id),
  id TEXT NOT NULL CHECK(length(id) BETWEEN 1 AND 64),
  label TEXT NOT NULL CHECK(length(label) BETWEEN 1 AND 80),
  key_hash TEXT NOT NULL CHECK(length(key_hash)=64),
  key_hint TEXT NOT NULL CHECK(length(key_hint)=4),
  primary_manager INTEGER NOT NULL CHECK(primary_manager IN (0,1)),
  created_at TEXT NOT NULL,
  PRIMARY KEY(site_id, id)
);
CREATE UNIQUE INDEX edge_managers_one_primary ON edge_managers(site_id) WHERE primary_manager=1;
