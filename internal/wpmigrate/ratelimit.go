package wpmigrate

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// SettingRatePerSecond is the default outbound request rate per source
	// (REST reads and media downloads); a migration's Options.RatePerSecond
	// overrides it.
	SettingRatePerSecond = "wpmigrate.rate_per_second"
	defaultRatePerSecond = 4.0
	maxRatePerSecond     = 1000.0 // cap of the setting (tests run unthrottled)
	// minOptionRate and maxOptionRate bound a migration's own
	// Options.RatePerSecond (the wizard field).
	minOptionRate = 1.0
	maxOptionRate = 20.0
)

// rateLimiter is a token bucket of capacity 1: each wait reserves the next
// slot, so n requests take at least (n-1)/rate. Waiters sleep outside the
// lock and give up when their context ends.
type rateLimiter struct {
	mu    sync.Mutex
	every time.Duration
	next  time.Time
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

func newRateLimiter(perSecond float64) *rateLimiter {
	l := &rateLimiter{now: time.Now, sleep: sleepCtx}
	l.setRate(perSecond)
	return l
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (l *rateLimiter) setRate(perSecond float64) {
	if perSecond <= 0 {
		perSecond = defaultRatePerSecond
	}
	perSecond = min(perSecond, maxRatePerSecond)
	l.mu.Lock()
	l.every = time.Duration(float64(time.Second) / perSecond)
	l.mu.Unlock()
}

// wait blocks until the caller's slot.
func (l *rateLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	slot := l.next
	if slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.every)
	l.mu.Unlock()
	if d := slot.Sub(now); d > 0 {
		return l.sleep(ctx, d)
	}
	return ctx.Err()
}

// rate is the outbound rate for a migration with opts: the option, else
// the wpmigrate.rate_per_second setting, else 4 per second.
func (s *service) rate(opts Options) float64 {
	if opts.RatePerSecond > 0 {
		return min(opts.RatePerSecond, maxRatePerSecond)
	}
	var v float64
	if s.settings != nil {
		if err := s.settings.Get(context.Background(), SettingRatePerSecond, &v); err == nil && v > 0 {
			return min(v, maxRatePerSecond)
		}
	}
	return defaultRatePerSecond
}

// limiter returns the shared bucket of a source (one per source for the
// whole process). rate only seeds a bucket that does not exist yet: a
// Discover (or any other caller) never retunes a running source's bucket.
func (s *service) limiter(sourceID string, rate float64) *rateLimiter {
	v, _ := s.limiters.LoadOrStore(sourceID, newRateLimiter(rate))
	return v.(*rateLimiter)
}

// tuneLimiter sets a source's bucket to rate; only starting a run (Start,
// or the dry-run preview) calls it, with that migration's rate.
func (s *service) tuneLimiter(sourceID string, rate float64) {
	s.limiter(sourceID, rate).setRate(rate)
}

// normalizeRate clamps a migration rate to [1, 20]; NaN, ±Inf, 0 and
// negative values mean unset (0), i.e. the setting, else the default 4/s.
func normalizeRate(r float64) float64 {
	if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 {
		return 0
	}
	return min(max(r, minOptionRate), maxOptionRate)
}

// parseRate validates the wizard's "Requests per second" field: blank is
// unset (0); otherwise a finite number from 1 to 20.
func parseRate(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	r, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(r) || math.IsInf(r, 0) || r < minOptionRate || r > maxOptionRate {
		return 0, false
	}
	return r, true
}
