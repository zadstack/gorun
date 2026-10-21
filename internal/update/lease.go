package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// LeaseScope is the one scope every data-directory operation competes for.
const LeaseScope = "data"

// Lease holders.
const (
	HolderUpdate  = "update"
	HolderBackup  = "backup"
	HolderRestore = "restore"
)

// ErrLeaseHeld reports that another operation owns the data directory.
type ErrLeaseHeld struct {
	Holder      string
	OperationID string
	ExpiresAt   string
}

func (e *ErrLeaseHeld) Error() string {
	return fmt.Sprintf("update: data directory busy: %s %s until %s", e.Holder, e.OperationID, e.ExpiresAt)
}

// ErrLeaseLost is returned by Renew when the caller no longer owns the lease.
var ErrLeaseLost = errors.New("update: lease lost")

// Stored expiries have second precision and are inclusive: a lease is live
// through its expiry second.
//
// Lease is the durable ownership record. A process-local mutex or a time
// cooldown cannot protect a restarted process; the row with its owner token
// and expiry can. Acquire takes a free or expired lease, or one already
// held by the same owner; Renew extends it only for its owner.
type Lease struct {
	db  *sql.DB
	now func() time.Time
	ttl time.Duration
}

// NewLease returns a lease on d with ttl (default 2 minutes).
func NewLease(d *sql.DB, ttl time.Duration) *Lease {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &Lease{db: d, now: time.Now, ttl: ttl}
}

// Holder describes the current lease.
type Holder struct {
	Holder, Owner, OperationID, ExpiresAt string
}

// Current returns the live lease, if any.
func (l *Lease) Current(ctx context.Context) (Holder, bool, error) {
	var h Holder
	err := l.db.QueryRowContext(ctx, `SELECT holder, owner, operation_id, expires_at FROM ops_leases WHERE scope = ?`, LeaseScope).Scan(&h.Holder, &h.Owner, &h.OperationID, &h.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Holder{}, false, nil
	}
	if err != nil {
		return Holder{}, false, err
	}
	if h.ExpiresAt < db.FormatTime(l.now()) {
		return h, false, nil
	}
	return h, true, nil
}

// Acquire takes the lease for owner (holder names the kind of work).
func (l *Lease) Acquire(ctx context.Context, holder, owner, operationID string) error {
	return db.InTx(ctx, l.db, func(tx *sql.Tx) error {
		return l.acquireTx(ctx, tx, holder, owner, operationID)
	})
}

// AcquireTx is Acquire inside tx.
func (l *Lease) AcquireTx(ctx context.Context, tx *sql.Tx, holder, owner, operationID string) error {
	return l.acquireTx(ctx, tx, holder, owner, operationID)
}

func (l *Lease) acquireTx(ctx context.Context, tx *sql.Tx, holder, owner, operationID string) error {
	now := l.now()
	nowS, exp := db.FormatTime(now), db.FormatTime(now.Add(l.ttl))
	var h Holder
	err := tx.QueryRowContext(ctx, `SELECT holder, owner, operation_id, expires_at FROM ops_leases WHERE scope = ?`, LeaseScope).Scan(&h.Holder, &h.Owner, &h.OperationID, &h.ExpiresAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case h.Owner != owner && h.ExpiresAt >= nowS:
		return &ErrLeaseHeld{Holder: h.Holder, OperationID: h.OperationID, ExpiresAt: h.ExpiresAt}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_leases (scope, holder, owner, operation_id, expires_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope) DO UPDATE SET holder = excluded.holder, owner = excluded.owner, operation_id = excluded.operation_id,
		expires_at = excluded.expires_at, updated_at = excluded.updated_at`, LeaseScope, holder, owner, operationID, exp, nowS)
	return err
}

// Renew extends the lease held by owner; ErrLeaseLost otherwise.
func (l *Lease) Renew(ctx context.Context, owner string) error {
	now := l.now()
	nowS := db.FormatTime(now)
	res, err := l.db.ExecContext(ctx, `UPDATE ops_leases SET expires_at = ?, updated_at = ? WHERE scope = ? AND owner = ? AND expires_at >= ?`,
		db.FormatTime(now.Add(l.ttl)), nowS, LeaseScope, owner, nowS)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Release frees the lease if owner holds it (a lost lease is not an error).
func (l *Lease) Release(ctx context.Context, owner string) error {
	_, err := l.db.ExecContext(ctx, `DELETE FROM ops_leases WHERE scope = ? AND owner = ?`, LeaseScope, owner)
	return err
}
