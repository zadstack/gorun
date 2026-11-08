package db

import (
	"context"
	"database/sql"
	"fmt"
)

// InTx runs fn inside a transaction (BEGIN IMMEDIATE, see Open). It commits
// when fn returns nil and rolls back on error or panic (re-panicking). Only
// SQLITE_BUSY at BeginTx is retried (three attempts, jittered backoff); fn and
// Commit run at most once. Each attempt retains the connection busy timeout,
// so lock acquisition may take about 15 seconds with the default 5s timeout.
// The caller context also governs backoff and the transaction lifetime.
//
// The transaction holds SQLite's write lock, so any write made through the
// pool (*sql.DB) from inside fn waits busy_timeout (5s) and fails. Inside fn
// write only through tx: use audit RecordTx, jobs EnqueueTx and settings
// SetTx; call Record, Enqueue, hooks Emit and Set only after InTx returns.
func InTx(ctx context.Context, d *sql.DB, fn func(tx *sql.Tx) error) (err error) {
	tx, err := beginTx(ctx, d)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}
