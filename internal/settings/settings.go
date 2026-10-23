// Package settings is the key/value settings store. Keys are namespaced
// ("site.title", "seo.default_image"); values are stored as JSON.
package settings

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/zadstack/gorun/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the settings table migrations ("migrations/NNNN_x.sql").
func Migrations() fs.FS { return migrations }

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("settings: not found")

// cacheEntry holds a raw JSON value, or marks a known-missing key.
type cacheEntry struct {
	raw string
	ok  bool // false = known missing (negative cache)
}

// Store reads and writes settings. Get/GetString results are memoized in
// process memory. The cache is dropped whenever settings_revision.n changes
// (triggers advance it on every committed settings write, from any
// connection or process, including e2e helpers that UPDATE settings
// directly) and on every Store write path.
type Store struct {
	db    *sql.DB
	log   *slog.Logger
	cache *settingsCache
}

type settingsCache struct {
	mu        sync.Mutex
	dbVersion int64 // settings_revision.n of the entries; 0 means "unknown / must re-sync"
	entries   map[string]cacheEntry
}

// New returns a store on d logging to slog.Default(). The settings
// migrations must already be applied.
func New(d *sql.DB) *Store {
	return &Store{
		db:    d,
		log:   slog.Default(),
		cache: &settingsCache{entries: map[string]cacheEntry{}},
	}
}

// WithLogger returns a copy of the store that logs to l, sharing the same
// process cache as s.
func (s *Store) WithLogger(l *slog.Logger) *Store {
	return &Store{db: s.db, log: l, cache: s.cache}
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Get unmarshals the JSON value of key into out, or returns ErrNotFound.
func (s *Store) Get(ctx context.Context, key string, out any) error {
	raw, err := s.raw(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("settings: decode %s: %w", key, err)
	}
	return nil
}

// revision reads the settings write counter. It replaces PRAGMA
// data_version, which is per connection: it ignores the connection's own
// commits, and two pool connections can report the same number across a
// write, so a raw write on the pool or a foreign write could leave a stale
// memo behind.
func (s *Store) revision(ctx context.Context) (int64, error) {
	var ver int64
	if err := s.db.QueryRowContext(ctx, `SELECT n FROM settings_revision WHERE id = 1`).Scan(&ver); err != nil {
		return 0, err
	}
	return ver, nil
}

// syncLocked drops the memo when a settings write (ours or any other
// connection's) advanced the revision. Caller must hold c.mu.
func (c *settingsCache) syncLocked(ver int64) {
	if ver == c.dbVersion {
		return
	}
	clear(c.entries)
	c.dbVersion = ver
}

func (s *Store) raw(ctx context.Context, key string) (string, error) {
	ver, err := s.revision(ctx)
	if err != nil {
		return "", fmt.Errorf("settings: revision: %w", err)
	}
	if s.cache == nil {
		var raw string
		err := s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("settings: get %s: %w", key, err)
		}
		return raw, nil
	}

	s.cache.mu.Lock()
	s.cache.syncLocked(ver)
	if e, hit := s.cache.entries[key]; hit {
		s.cache.mu.Unlock()
		if !e.ok {
			return "", ErrNotFound
		}
		return e.raw, nil
	}
	fillVer := s.cache.dbVersion
	s.cache.mu.Unlock()

	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		s.remember(key, "", false, fillVer)
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("settings: get %s: %w", key, err)
	}
	s.remember(key, raw, true, fillVer)
	return raw, nil
}

// remember stores a fill only when the revision is still fillVer (a Store
// write or foreign SQL write clears/advances the cache in between).
func (s *Store) remember(key, raw string, ok bool, fillVer int64) {
	if s.cache == nil {
		return
	}
	s.cache.mu.Lock()
	if s.cache.dbVersion == fillVer {
		s.cache.entries[key] = cacheEntry{raw: raw, ok: ok}
	}
	s.cache.mu.Unlock()
}

// Invalidate drops the process memo. Safe after out-of-band SQL writes when
// callers mutate the DB outside Store; write paths call this too.
func (s *Store) Invalidate() {
	if s.cache == nil {
		return
	}
	s.cache.mu.Lock()
	clear(s.cache.entries)
	s.cache.dbVersion = 0
	s.cache.mu.Unlock()
}

// GetString returns the string value of key, or def when it is missing.
// Other failures (DB errors, non-string values) are logged and return def.
func (s *Store) GetString(ctx context.Context, key, def string) string {
	var v string
	if err := s.Get(ctx, key, &v); err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("settings: get string", "key", key, "err", err)
		}
		return def
	}
	return v
}

// Set stores v (marshalled to JSON) under key, replacing any previous value.
//
// Set writes on its own pool connection: never call it inside db.InTx (it
// would wait on the transaction's lock for busy_timeout and fail). Use SetTx
// inside a transaction, or call Set after commit.
func (s *Store) Set(ctx context.Context, key string, v any) error {
	return s.set(ctx, s.db, key, v)
}

// SetTx is Set inside tx.
func (s *Store) SetTx(ctx context.Context, tx *sql.Tx, key string, v any) error {
	return s.set(ctx, tx, key, v)
}

func (s *Store) set(ctx context.Context, ex execer, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("settings: encode %s: %w", key, err)
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO settings (key, value_json, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		key, string(raw), db.Now())
	if err != nil {
		return fmt.Errorf("settings: set %s: %w", key, err)
	}
	s.Invalidate()
	return nil
}

// All returns every setting whose key starts with prefix ("" for all).
func (s *Store) All(ctx context.Context, prefix string) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value_json FROM settings WHERE substr(key, 1, length(?)) = ? ORDER BY key`, prefix, prefix)
	if err != nil {
		return nil, fmt.Errorf("settings: list %q: %w", prefix, err)
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("settings: scan: %w", err)
		}
		out[k] = json.RawMessage(v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes key; a missing key is not an error. Like Set, never call
// it inside db.InTx.
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("settings: delete %s: %w", key, err)
	}
	s.Invalidate()
	return nil
}

// DeletePrefix removes every key starting with prefix (e.g. "ext.shop.").
// An empty prefix is refused: it would delete every setting. Never call it
// inside db.InTx; use DeletePrefixTx there.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	return s.deletePrefix(ctx, s.db, prefix)
}

// DeletePrefixTx is DeletePrefix inside tx.
func (s *Store) DeletePrefixTx(ctx context.Context, tx *sql.Tx, prefix string) error {
	return s.deletePrefix(ctx, tx, prefix)
}

func (s *Store) deletePrefix(ctx context.Context, ex execer, prefix string) error {
	if prefix == "" {
		return errors.New("settings: delete prefix: empty prefix refused")
	}
	if _, err := ex.ExecContext(ctx, `DELETE FROM settings WHERE substr(key, 1, length(?)) = ?`, prefix, prefix); err != nil {
		return fmt.Errorf("settings: delete prefix %q: %w", prefix, err)
	}
	s.Invalidate()
	return nil
}
