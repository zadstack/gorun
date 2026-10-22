package jobs

import (
	"context"
	"time"
)

// Cron runs fn roughly every `every`, checked on the scheduler tick (30s),
// so intervals shorter than the tick are rounded up to it. The first tick
// after Start runs every cron. Register crons before Start.
func (q *Queue) Cron(name string, every time.Duration, fn func(ctx context.Context) error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.crons = append(q.crons, &cronEntry{name: name, every: every, fn: fn})
}

// CronNames returns the registered cron names in registration order.
func (q *Queue) CronNames() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]string, 0, len(q.crons))
	for _, c := range q.crons {
		out = append(out, c.name)
	}
	return out
}

func (q *Queue) schedule(ctx context.Context) {
	t := time.NewTicker(q.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			q.recover(ctx)
			q.tick(ctx)
		}
	}
}

// tick runs every cron whose interval has elapsed since its last run.
func (q *Queue) tick(ctx context.Context) {
	now := q.now()
	q.mu.Lock()
	var due []*cronEntry
	for _, c := range q.crons {
		if c.lastRun.IsZero() || now.Sub(c.lastRun) >= c.every {
			c.lastRun = now
			due = append(due, c)
		}
	}
	q.mu.Unlock()
	for _, c := range due {
		if ctx.Err() != nil {
			return
		}
		if err := c.fn(ctx); err != nil {
			q.log.Error("jobs: cron failed", "cron", c.name, "err", err)
		}
	}
}
