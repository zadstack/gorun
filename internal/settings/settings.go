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
	"strings"
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
// process memory and invalidated on Set/Delete/DeletePrefix so hot public
// renders do not round-trip SQLite for every {{setting}} / siteInfo / SEO key.
type Store struct {
	db    *sql.DB
	log   *slog.Logger
	cache *settingsCache
}

type settingsCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
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

func (s *Store) raw(ctx context.Context, key string) (string, error) {
	if s.cache != nil {
		s.cache.mu.RLock()
		if e, hit := s.cache.entries[key]; hit {
			s.cache.mu.RUnlock()
			if !e.ok {
				return "", ErrNotFound
			}
			return e.raw, nil
		}
		s.cache.mu.RUnlock()
	}

	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		s.remember(key, "", false)
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("settings: get %s: %w", key, err)
	}
	s.remember(key, raw, true)
	return raw, nil
}

func (s *Store) remember(key, raw string, ok bool) {
	if s.cache == nil {
		return
	}
	s.cache.mu.Lock()
	s.cache.entries[key] = cacheEntry{raw: raw, ok: ok}
	s.cache.mu.Unlock()
}

func (s *Store) forget(key string) {
	if s.cache == nil {
		return
	}
	s.cache.mu.Lock()
	delete(s.cache.entries, key)
	s.cache.mu.Unlock()
}

func (s *Store) forgetPrefix(prefix string) {
	if s.cache == nil {
		return
	}
	s.cache.mu.Lock()
	for k := range s.cache.entries {
		if strings.HasPrefix(k, prefix) {
			delete(s.cache.entries, k)
		}
	}
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
	// Drop the entry so the next Get observes the writer (or a rolled-back
	// SetTx still hits SQLite for the committed value).
	s.forget(key)
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
		s.remember(k, v, true)
	}
	return out, rows.Err()
}

// Delete removes key; a missing key is not an error. Like Set, never call
// it inside db.InTx.
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("settings: delete %s: %w", key, err)
	}
	s.forget(key)
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
	s.forgetPrefix(prefix)
	return nil
}
