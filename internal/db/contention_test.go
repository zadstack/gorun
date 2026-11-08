package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Two independent pools reproduce writers competing for the same file. A zero
// timeout makes SQLite report contention immediately so tests exercise backoff
// rather than spending the production five seconds inside the driver.
func competingWriters(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contention.db")
	owner, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	if _, err := owner.Exec(`CREATE TABLE writes (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	uri, err := FileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	contender, err := sql.Open("sqlite", uri+"?_txlock=immediate&_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	contender.SetMaxOpenConns(1)
	t.Cleanup(func() { contender.Close() })
	return owner, contender
}

func awaitBusy(t *testing.T, before uint64) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for Contention().Busy == before {
		select {
		case <-timer.C:
			t.Fatal("writer did not report lock contention")
		case <-tick.C:
		}
	}
}

func TestInTxBusyBeginRecoversWithoutReplayingWrite(t *testing.T) {
	owner, contender := competingWriters(t)
	lock, err := owner.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	before := Contention()
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- InTx(t.Context(), contender, func(tx *sql.Tx) error {
			calls.Add(1)
			_, err := tx.Exec(`INSERT INTO writes VALUES (1)`)
			return err
		})
	}()
	awaitBusy(t, before.Busy)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := owner.QueryRow(`SELECT COUNT(*) FROM writes`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || calls.Load() != 1 || Contention().Retries <= before.Retries {
		t.Fatalf("rows=%d calls=%d counters=%+v", rows, calls.Load(), Contention())
	}
}

func TestInTxBusyBeginExhaustsBoundedAttempts(t *testing.T) {
	owner, contender := competingWriters(t)
	lock, err := owner.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	before := Contention()
	calls := 0
	err = InTx(t.Context(), contender, func(*sql.Tx) error { calls++; return nil })
	after := Contention()
	if !sqliteBusy(err) || calls != 0 || after.Busy-before.Busy != 3 || after.Retries-before.Retries != 2 || after.Exhausted-before.Exhausted != 1 {
		t.Fatalf("err=%v calls=%d before=%+v after=%+v", err, calls, before, after)
	}
}

func TestInTxCancellationStopsBusyBackoff(t *testing.T) {
	owner, contender := competingWriters(t)
	lock, err := owner.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	before := Contention()
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- InTx(ctx, contender, func(*sql.Tx) error { calls.Add(1); return nil })
	}()
	awaitBusy(t, before.Busy)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop lock acquisition")
	}
}

func TestInTxCallbackBusyRollsBackWithoutReplay(t *testing.T) {
	owner, contender := competingWriters(t)
	calls := 0
	before := Contention()
	err := InTx(t.Context(), owner, func(tx *sql.Tx) error {
		calls++
		if _, err := tx.Exec(`INSERT INTO writes VALUES (1)`); err != nil {
			return err
		}
		// A second connection cannot write while this callback owns the lock.
		_, err := contender.Exec(`INSERT INTO writes VALUES (2)`)
		return err
	})
	var rows int
	if queryErr := owner.QueryRow(`SELECT COUNT(*) FROM writes`).Scan(&rows); queryErr != nil {
		t.Fatal(queryErr)
	}
	if !sqliteBusy(err) || calls != 1 || rows != 0 || Contention() != before {
		t.Fatalf("err=%v calls=%d rows=%d counters=%+v", err, calls, rows, Contention())
	}
}

func TestInTxNonBusyBeginErrorIsNotRetried(t *testing.T) {
	_, contender := competingWriters(t)
	contender.Close()
	before := Contention()
	calls := 0
	err := InTx(t.Context(), contender, func(*sql.Tx) error { calls++; return nil })
	if err == nil || calls != 0 || Contention() != before {
		t.Fatalf("err=%v calls=%d counters=%+v", err, calls, Contention())
	}
}

func TestInTxDeadlineInterruptsDriverBusyWait(t *testing.T) {
	owner, _ := competingWriters(t)
	// Use the production busy timeout rather than the zero-timeout test pool.
	conn, err := owner.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lock, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	calls := 0
	started := time.Now()
	err = InTx(ctx, owner, func(*sql.Tx) error { calls++; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("deadline waited for the 5s driver busy timeout: %s", elapsed)
	}
}

func TestInTxCommitFailureDoesNotReplayOrRetainWriteLock(t *testing.T) {
	owner, _ := competingWriters(t)
	owner.SetMaxOpenConns(1)
	if _, err := owner.Exec(`CREATE TABLE parents (id INTEGER PRIMARY KEY);
		CREATE TABLE children (parent_id INTEGER REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	before := Contention()
	calls := 0
	err := InTx(t.Context(), owner, func(tx *sql.Tx) error {
		calls++
		_, err := tx.Exec(`INSERT INTO children VALUES (1)`)
		return err
	})
	if err == nil || calls != 1 || Contention() != before {
		t.Fatalf("err=%v calls=%d counters=%+v", err, calls, Contention())
	}
	var rows int
	if err := owner.QueryRow(`SELECT COUNT(*) FROM children`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed commit left rows=%d err=%v", rows, err)
	}
	if err := InTx(t.Context(), owner, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO parents VALUES (1)`)
		return err
	}); err != nil {
		t.Fatalf("connection was not usable after commit failure: %v", err)
	}
}
