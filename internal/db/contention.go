package db

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const beginAttempts = 3

var beginBusy, beginRetries, beginExhausted atomic.Uint64

// ContentionStats reports process-wide InTx lock acquisition counters. Direct
// SQL operations and transactions opened outside InTx are not included.
type ContentionStats struct {
	Busy      uint64
	Retries   uint64
	Exhausted uint64
}

// Contention returns a concurrency-safe snapshot of lock acquisition counters.
func Contention() ContentionStats {
	return ContentionStats{Busy: beginBusy.Load(), Retries: beginRetries.Load(), Exhausted: beginExhausted.Load()}
}

// beginTx retries only before a transaction exists: replaying fn or Commit
// could duplicate external effects or writes with an uncertain outcome.
func beginTx(ctx context.Context, d *sql.DB) (*sql.Tx, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			beginRetries.Add(1)
		}
		tx, err := d.BeginTx(ctx, nil)
		if !sqliteBusy(err) {
			return tx, err
		}
		beginBusy.Add(1)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == beginAttempts-1 {
			beginExhausted.Add(1)
			return nil, err
		}
		if err := waitForWriter(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

func sqliteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY
}

func waitForWriter(ctx context.Context, attempt int) error {
	base := (25 * time.Millisecond) << attempt
	timer := time.NewTimer(base + time.Duration(rand.Int64N(int64(base))))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
