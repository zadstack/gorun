package wpmigrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLimiter returns a limiter on a fake clock whose sleeps advance it.
func fakeLimiter(rate float64) (*rateLimiter, *time.Time) {
	l := newRateLimiter(rate)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	l.sleep = func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil }
	return l, &clock
}

// I9: at 4 requests/second, 20 requests take at least ~4.5 s.
func TestRateLimiterBound(t *testing.T) {
	l, clock := fakeLimiter(4)
	start := *clock
	for i := 0; i < 20; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := clock.Sub(start); got < 4500*time.Millisecond || got > 5*time.Second {
		t.Fatalf("20 requests at 4/s took %v, want ≈4.75s", got)
	}
	// A cancelled context stops the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	real := newRateLimiter(1)
	_ = real.wait(context.Background())
	if err := real.wait(ctx); err == nil {
		t.Fatal("a cancelled wait must fail")
	}
}

// The rate comes from the migration option, then the setting, then 4/s;
// one bucket per source is shared by REST reads and media downloads.
func TestRateResolutionAndSharedBucket(t *testing.T) {
	e := newEnv(t)
	s := e.svc
	ctx := context.Background()
	if r := s.rate(Options{RatePerSecond: 2}); r != 2 {
		t.Fatalf("option rate = %v", r)
	}
	if err := e.app.Deps.Settings.Set(ctx, SettingRatePerSecond, 7); err != nil {
		t.Fatal(err)
	}
	if r := s.rate(Options{}); r != 7 {
		t.Fatalf("setting rate = %v", r)
	}
	if err := e.app.Deps.Settings.Delete(ctx, SettingRatePerSecond); err != nil {
		t.Fatal(err)
	}
	if r := s.rate(Options{}); r != defaultRatePerSecond {
		t.Fatalf("default rate = %v", r)
	}
	if s.limiter("src1", 4) != s.limiter("src1", 4) || s.limiter("src1", 4) == s.limiter("src2", 4) {
		t.Fatal("one bucket per source")
	}

	// Real time: a REST client and media downloads of one source share
	// the bucket — 3 REST reads + 3 downloads at 10/s take ≥ 0.5 s.
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	src := &sourceRow{ID: "src-rate", URL: srv.URL}
	opts := Options{RatePerSecond: 10}
	c, err := s.client(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	m := &migrationRow{SourceID: src.ID, Options: opts}
	begin := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.Get(ctx, "/wp/v2/posts", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.download(ctx, m, srv.URL+"/wp-content/uploads/x.png"); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(begin); el < 450*time.Millisecond {
		t.Fatalf("6 requests at 10/s took %v", el)
	}
	if hits.Load() != 6 {
		t.Fatalf("hits = %d", hits.Load())
	}
}
