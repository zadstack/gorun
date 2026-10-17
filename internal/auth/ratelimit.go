package auth

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const (
	maxLoginFailures   = 5
	maxLoginIPAttempts = 30
	maxPasswordChecks  = 4
	loginWindow        = 15 * time.Minute
	defaultKeyRate     = 60 // requests per minute
)

// Bound memory used by simultaneous Argon2 login checks across services.
var passwordChecks = make(chan struct{}, maxPasswordChecks)

// loginLimiter counts failed logins per key in login_attempts, so lockouts
// survive restarts.
type loginLimiter struct {
	db  *sql.DB
	now func() time.Time
}

func newLoginLimiter(d *sql.DB) *loginLimiter { return &loginLimiter{db: d, now: time.Now} }

// hit atomically counts one attempt for key and returns the count within the
// current 15-minute window (a window older than that restarts at 1).
func (l *loginLimiter) hit(ctx context.Context, key string) (int, error) {
	now := l.now()
	cutoff := db.FormatTime(now.Add(-loginWindow))
	var count int
	var start string
	err := l.db.QueryRowContext(ctx, `INSERT INTO login_attempts (key, count, window_start) VALUES (?, 1, ?)
		ON CONFLICT (key) DO UPDATE SET
			count = CASE WHEN login_attempts.window_start <= ? THEN 1 ELSE login_attempts.count + 1 END,
			window_start = CASE WHEN login_attempts.window_start <= ? THEN excluded.window_start ELSE login_attempts.window_start END
		RETURNING count, window_start`, key, db.FormatTime(now), cutoff, cutoff).Scan(&count, &start)
	if err != nil {
		return 0, fmt.Errorf("auth: count login attempt: %w", err)
	}
	return count, nil
}

func (l *loginLimiter) reset(ctx context.Context, key string) error {
	if _, err := l.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE key = ?`, key); err != nil {
		return fmt.Errorf("auth: reset login attempts: %w", err)
	}
	return nil
}

// bucketLimiter is an in-memory token bucket per key (API keys): capacity
// and refill are rate per minute.
type bucketLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newBucketLimiter() *bucketLimiter {
	return &bucketLimiter{buckets: map[string]*bucket{}, now: time.Now}
}

// allow takes one token for key; when empty it returns the wait until the
// next token.
func (b *bucketLimiter) allow(key string, perMin int) (bool, time.Duration) {
	if perMin <= 0 {
		perMin = defaultKeyRate
	}
	capacity, perSec := float64(perMin), float64(perMin)/60
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	bk, ok := b.buckets[key]
	if !ok {
		bk = &bucket{tokens: capacity, last: now}
		b.buckets[key] = bk
	}
	bk.tokens = math.Min(capacity, bk.tokens+now.Sub(bk.last).Seconds()*perSec)
	bk.last = now
	if bk.tokens >= 1 {
		bk.tokens--
		return true, 0
	}
	return false, time.Duration((1 - bk.tokens) / perSec * float64(time.Second))
}
