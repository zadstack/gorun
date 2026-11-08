package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestBucketLimiterReclaimsIdleKeysWithoutResettingActiveBudget(t *testing.T) {
	limiter := newBucketLimiter()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for i := range 1000 {
		if ok, _ := limiter.allow(fmt.Sprint(i), 1); !ok {
			t.Fatal("new key should have its initial token")
		}
	}
	now = now.Add(59 * time.Second)
	if ok, _ := limiter.allow("active", 1); !ok {
		t.Fatal("new active key should have its initial token")
	}
	now = now.Add(2 * time.Second)
	if ok, wait := limiter.allow("active", 1); ok || wait != 58*time.Second {
		t.Fatalf("cleanup reset an active budget: allowed=%v wait=%s", ok, wait)
	}
	if n := len(limiter.buckets); n != 1 {
		t.Fatalf("idle keys retained in memory: %d buckets, want only active key", n)
	}
}
