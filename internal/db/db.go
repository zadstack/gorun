// Package db opens the GoRun SQLite database and provides migration,
// transaction, ID and time helpers shared by every module.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, CGO-free)
)

// maxOpenConns sizes the single connection pool. Writers serialise through
// SQLite's lock; busy_timeout plus BEGIN IMMEDIATE (_txlock) make them wait
// instead of failing with SQLITE_BUSY.
const maxOpenConns = 8

// dsnQuery holds the connection pragmas (applied to every pooled connection
// by the driver) and the immediate-transaction lock mode.
const dsnQuery = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_txlock=immediate"

// Open opens (creating if needed) the SQLite database at path with WAL,
// busy_timeout=5000, foreign_keys=ON, synchronous=NORMAL and immediate
// transactions. The parent directory is created when missing.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("db: create dir: %w", err)
		}
	}
	dsn, err := FileURI(path)
	if err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", dsn+dsnQuery)
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

// FileURI returns path as an SQLite "file:" URI with the path
// percent-encoded, so characters such as '?', '#', '%' and spaces are part
// of the file name instead of starting the query (which carries the
// pragmas) or a fragment. The path is made absolute first (file:///abs).
func FileURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("db: resolve path %s: %w", path, err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if u.Path != "" && u.Path[0] != '/' { // Windows drive letter: file:///C:/...
		u.Path = "/" + u.Path
	}
	return u.String(), nil
}
