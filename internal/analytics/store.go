package analytics

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DBFile is the analytics database file name inside the data directory.
// internal/backup finds the optional backup member by this name (ruling A2).
const DBFile = "analytics.db"

// migrationModule is the schema_migrations key inside analytics.db.
const migrationModule = "analytics"

// InsertBatch is the largest number of rows Insert writes in one transaction.
const InsertBatch = 500

// Event kinds (events.kind).
const (
	KindView   = "view"
	KindSubmit = "submit"
)

// Event is one stored row of events. TS is stored in UTC at second
// precision (db.FormatTime); the other fields are stored as given, so the
// caller strips query strings, caps the path and hashes the visitor before
// the event reaches the buffer.
type Event struct {
	TS                                time.Time
	Kind, Path, Visitor, RefHost      string
	UTMSource, UTMMedium, UTMCampaign string
	Browser, OS, Device, Country      string
	Locale, Form                      string
}

// Store owns analytics.db. It is safe for concurrent use. DB returns the
// current handle, which Reopen replaces, so callers fetch it per use and do
// not keep it.
type Store struct {
	path string

	mu     sync.RWMutex
	db     *sql.DB
	closed bool // Close was called: no reopen, every call fails with ErrClosed

	saltMu sync.Mutex
	salts  map[string][]byte // day (YYYY-MM-DD) -> salt, cache of the salts table

	onTx func() // test hook: called once per committed Insert transaction
}

// OpenStore opens (creating it when missing) <dataDir>/analytics.db and
// applies the module's migrations to it.
func OpenStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, DBFile), salts: map[string][]byte{}}
	d, err := openDB(s.path)
	if err != nil {
		return nil, err
	}
	s.db = d
	return s, nil
}

func openDB(path string) (*sql.DB, error) {
	d, err := db.Open(path)
	if err != nil {
		return nil, fmt.Errorf("analytics: open %s: %w", path, err)
	}
	if err := db.Migrate(context.Background(), d, migrationModule, migrations); err != nil {
		d.Close()
		return nil, fmt.Errorf("analytics: migrate %s: %w", path, err)
	}
	return d, nil
}

// Path is the analytics.db file path.
func (s *Store) Path() string { return s.path }

// DB returns the current analytics.db handle. Like Insert it recreates a
// file deleted while running, so reads see an empty database instead of the
// unlinked one. It returns nil after Close; when recreating fails it
// returns the handle it has (possibly nil) and the query reports the error.
func (s *Store) DB() *sql.DB {
	if d, err := s.ensure(); err == nil {
		return d
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// ErrClosed is returned by Store methods called after Close.
var ErrClosed = errors.New("analytics: store closed")

// Close closes analytics.db. The store stays closed: later calls fail with
// ErrClosed instead of recreating the file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Reopen closes the current handle and opens the file again, creating and
// migrating it when it is missing. It is the recreate path for a file that
// was deleted while running, and what a restore uses to pick up the new file.
func (s *Store) Reopen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopenLocked()
}

func (s *Store) reopenLocked() error {
	if s.closed {
		return ErrClosed
	}
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	if _, err := os.Stat(s.path); errors.Is(err, fs.ErrNotExist) {
		// A WAL left beside a deleted file belongs to the old database;
		// SQLite would replay it into the new empty one.
		_ = os.Remove(s.path + "-wal")
		_ = os.Remove(s.path + "-shm")
	}
	d, err := openDB(s.path)
	if err != nil {
		return err
	}
	s.db = d
	s.saltMu.Lock()
	s.salts = map[string][]byte{}
	s.saltMu.Unlock()
	return nil
}

// ensure returns a handle to a file that exists on disk: when analytics.db
// was deleted (an open handle would keep writing to the unlinked inode) or
// an earlier reopen failed, it reopens, recreating the file.
func (s *Store) ensure() (*sql.DB, error) {
	s.mu.RLock()
	d := s.db
	s.mu.RUnlock()
	if d != nil {
		if _, err := os.Stat(s.path); err == nil {
			return d, nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.db != nil && s.db != d { // another caller already reopened
		return s.db, nil
	}
	if err := s.reopenLocked(); err != nil {
		return nil, err
	}
	return s.db, nil
}

const insertSQL = `INSERT INTO events (ts, kind, path, visitor, ref_host, utm_source, utm_medium, utm_campaign, browser, os, device, country, locale, form)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Insert stores evs, InsertBatch rows per transaction. A batch that fails
// stops the call; earlier batches stay committed.
func (s *Store) Insert(ctx context.Context, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	d, err := s.ensure()
	if err != nil {
		return err
	}
	for start := 0; start < len(evs); start += InsertBatch {
		end := min(start+InsertBatch, len(evs))
		if err := s.insertTx(ctx, d, evs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) insertTx(ctx context.Context, d *sql.DB, evs []Event) error {
	err := db.InTx(ctx, d, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, insertSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range evs {
			if _, err := stmt.ExecContext(ctx, db.FormatTime(e.TS), e.Kind, e.Path, e.Visitor, e.RefHost, e.UTMSource, e.UTMMedium, e.UTMCampaign,
				e.Browser, e.OS, e.Device, e.Country, e.Locale, e.Form); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("analytics: insert %d events: %w", len(evs), err)
	}
	if s.onTx != nil {
		s.onTx()
	}
	return nil
}
