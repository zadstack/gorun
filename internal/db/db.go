// Package db opens the GoRun SQLite database and provides migration,
// transaction, ID and time helpers shared by every module.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, CGO-free)
)

// maxOpenConns sizes the single connection pool. Writers serialise through
// SQLite's lock; busy_timeout plus BEGIN IMMEDIATE (_txlock) make them wait
// instead of failing with SQLITE_BUSY.
const maxOpenConns = 8

// Open opens (creating if needed) the SQLite database at path with WAL,
// busy_timeout=5000, foreign_keys=ON, synchronous=NORMAL and immediate
// transactions. The parent directory is created when missing.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("db: create dir: %w", err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	d.SetMaxOpenConns(maxOpenConns)
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("db: ping %s: %w", path, err)
	}
	return d, nil
}
