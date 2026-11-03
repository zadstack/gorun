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

// Store owns analytics.db. It is safe for concurrent use.
//
// Every use of the handle (Insert, VisitorHash, With) holds the read lock
// for its whole duration, and Reopen takes the write lock, so the old handle
// is closed only once no connection of it is in use. Closing an in-use
// connection later would let SQLite's WAL close unlink the -wal and -shm
// files by path, which by then belong to the recreated database.
type Store struct {
	path string

	mu     sync.RWMutex
	db     *sql.DB // never nil once OpenStore returned; closed after Close or a failed reopen
	broken bool    // the last reopen failed: db is closed, the next use retries
	closed bool    // Close was called: no reopen, every use fails with ErrClosed

	saltMu   sync.Mutex // inside mu (read or write), never the other way round
	saltDay  string     // newest day with a salt (YYYY-MM-DD), cached; "" unknown
	saltData []byte

	now func() time.Time // the queries' clock (which day is today); nil is time.Now

	onTx func()       // test hook: called once per committed Insert transaction
	inTx func() error // test hook: called inside each Insert transaction, after the rows
}

// OpenStore opens (creating it when missing) <dataDir>/analytics.db and
// applies the module's migrations to it.
func OpenStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, DBFile)}
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

// ErrClosed is returned by Store methods called after Close.
var ErrClosed = errors.New("analytics: store closed")

// With runs fn with the analytics.db handle while holding the store's read
// lock, recreating the file first when it was deleted, so a read after a
// file loss sees an empty database. fn must not keep the handle, and must
// not call other Store methods (the lock is not reentrant).
func (s *Store) With(ctx context.Context, fn func(*sql.DB) error) error {
	d, release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()
	return fn(d)
}

// DB returns the current handle without holding any lock: a Reopen may
// close it at any moment. It exists for tests and diagnostics; everything
// else uses With. It is never nil (a closed handle reports
// "sql: database is closed").
func (s *Store) DB() *sql.DB {
	if _, release, err := s.acquire(); err == nil { // recreate a lost file, as With does
		release()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// Close closes analytics.db, waiting for in-flight uses. The store stays
// closed: later calls fail with ErrClosed instead of recreating the file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.broken {
		return nil
	}
	return s.db.Close()
}

// Reopen waits for in-flight uses, closes the handle and opens the file
// again, creating and migrating it when it is missing. It is the recreate
// path for a file deleted while running, and what a restore uses to pick
// up a replaced file.
func (s *Store) Reopen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopenLocked()
}

// reopenLocked needs s.mu held for writing: no connection of the old handle
// is in use, so Close closes them all now, before the files are touched.
func (s *Store) reopenLocked() error {
	if s.closed {
		return ErrClosed
	}
	if !s.broken {
		_ = s.db.Close()
		s.broken = true
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
	s.db, s.broken = d, false
	s.saltMu.Lock()
	s.saltDay, s.saltData = "", nil
	s.saltMu.Unlock()
	return nil
}

// acquire returns a handle to a file that exists on disk, holding the read
// lock until release is called. When analytics.db was deleted (an open
// handle would keep using the unlinked inode) or the last reopen failed,
// it reopens under the write lock first.
func (s *Store) acquire() (*sql.DB, func(), error) {
	for attempt := 0; ; attempt++ {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return nil, nil, ErrClosed
		}
		if !s.broken && s.exists() {
			return s.db, s.mu.RUnlock, nil
		}
		s.mu.RUnlock()
		if attempt > 0 { // reopened, yet the file is gone again or still broken
			return nil, nil, fmt.Errorf("analytics: %s unavailable after reopen", s.path)
		}
		s.mu.Lock()
		var err error
		if s.closed {
			err = ErrClosed
		} else if s.broken || !s.exists() { // nobody reopened while we waited
			err = s.reopenLocked()
		}
		s.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
	}
}

func (s *Store) exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

const insertSQL = `INSERT INTO events (ts, kind, path, visitor, ref_host, utm_source, utm_medium, utm_campaign, browser, os, device, country, locale, form)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Insert stores evs, InsertBatch rows per transaction, holding the read
// lock throughout. A batch that fails stops the call; earlier batches stay
// committed.
func (s *Store) Insert(ctx context.Context, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	d, release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()
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
		if s.inTx != nil {
			return s.inTx()
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
