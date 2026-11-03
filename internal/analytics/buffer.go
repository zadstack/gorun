package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Write buffer defaults (plan, Global Constraints).
const (
	BufferCap   = 10000           // events held in memory before Add drops
	FlushEvery  = 2 * time.Second // Run's flush interval
	FlushBatch  = 500             // rows per Insert call (and per transaction)
	stopTimeout = 10 * time.Second
)

// dropWarnEvery rate-limits the "buffer full" warning.
const dropWarnEvery = time.Minute

// Buffer queues events in memory and writes them to the store in batches,
// so a burst of page views costs one transaction per batch instead of one
// per request. When it holds cap events, Add drops new ones. It is safe for
// concurrent use.
type Buffer struct {
	s     *Store
	cap   int
	every time.Duration
	batch int
	log   *slog.Logger
	now   func() time.Time

	mu       sync.Mutex
	q        []Event
	dropped  int       // drops since the last warning
	lastWarn time.Time // zero: never warned
	flushMu  sync.Mutex
}

// NewBuffer returns a buffer of capacity cap that Run flushes every every,
// batch events per Insert (at most InsertBatch). Non-positive values take
// the defaults above; log may be nil (slog.Default).
func NewBuffer(s *Store, cap int, every time.Duration, batch int, log *slog.Logger) *Buffer {
	if cap < 1 {
		cap = BufferCap
	}
	if every <= 0 {
		every = FlushEvery
	}
	if batch < 1 || batch > InsertBatch {
		batch = min(FlushBatch, InsertBatch) // one transaction per batch, so a failed batch requeues without duplicates
	}
	if log == nil {
		log = slog.Default()
	}
	return &Buffer{s: s, cap: cap, every: every, batch: batch, log: log, now: time.Now}
}

// Add queues e and reports whether it was kept. A zero TS is set to the
// time of the call, so an event keeps the day it happened on however late it
// is flushed. When the buffer is full the event is dropped and a warning is
// logged at most once per minute, with the number dropped since the last one.
func (b *Buffer) Add(e Event) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if len(b.q) >= b.cap {
		b.dropped++
		if b.lastWarn.IsZero() || now.Sub(b.lastWarn) >= dropWarnEvery {
			b.log.Warn("analytics: buffer full, events dropped", "dropped", b.dropped, "cap", b.cap)
			b.lastWarn, b.dropped = now, 0
		}
		return false
	}
	if e.TS.IsZero() {
		e.TS = now
	}
	b.q = append(b.q, e)
	return true
}

// Len returns the number of queued events.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.q)
}

// Flush writes every queued event, batch events per Insert. Events of a
// batch that fails go back to the front of the queue as far as the capacity
// allows, and the error is returned.
func (b *Buffer) Flush(ctx context.Context) error {
	b.flushMu.Lock() // one flush at a time keeps the requeue in order
	defer b.flushMu.Unlock()
	b.mu.Lock()
	pending := b.q
	b.q = nil
	b.mu.Unlock()
	for start := 0; start < len(pending); start += b.batch {
		end := min(start+b.batch, len(pending))
		if err := b.s.Insert(ctx, pending[start:end]); err != nil {
			b.requeue(pending[start:])
			return err
		}
	}
	return nil
}

// requeue puts failed events back ahead of those added since the flush
// began, dropping the newest beyond the capacity.
func (b *Buffer) requeue(failed []Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	merged := make([]Event, 0, min(len(failed)+len(b.q), b.cap))
	merged = append(merged, failed[:min(len(failed), b.cap)]...)
	room := b.cap - len(merged)
	merged = append(merged, b.q[:min(len(b.q), room)]...)
	if lost := len(failed) + len(b.q) - len(merged); lost > 0 {
		b.log.Warn("analytics: buffer full after a failed flush, events dropped", "dropped", lost)
	}
	b.q = merged
}

// Run flushes every interval until ctx is cancelled, then flushes what is
// left (with a fresh deadline, since ctx is already done) and returns.
func (b *Buffer) Run(ctx context.Context) {
	t := time.NewTicker(b.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
			if err := b.Flush(fctx); err != nil {
				b.log.Error("analytics: final flush failed, events lost", "err", err, "events", b.Len())
			}
			cancel()
			return
		case <-t.C:
			if err := b.Flush(ctx); err != nil && ctx.Err() == nil {
				b.log.Error("analytics: flush failed", "err", err)
			}
		}
	}
}
