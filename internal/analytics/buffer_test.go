package analytics

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestBufferDropsOnOverflow(t *testing.T) {
	s, _ := openTestStore(t)
	var logs syncBuf
	log := slog.New(slog.NewTextHandler(&logs, nil))
	b := NewBuffer(s, 3, time.Hour, 500, log)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	ev := Event{TS: now, Kind: KindView, Path: "/"}
	for i := 0; i < 3; i++ {
		if !b.Add(ev) {
			t.Fatalf("add %d dropped", i+1)
		}
	}
	if b.Add(ev) {
		t.Fatal("4th add accepted with cap 3")
	}
	for i := 0; i < 10; i++ {
		b.Add(ev)
	}
	if n := strings.Count(logs.String(), "analytics: buffer full"); n != 1 {
		t.Fatalf("%d overflow warnings within one interval, want 1:\n%s", n, logs.String())
	}
	now = now.Add(dropWarnEvery)
	b.Add(ev)
	if n := strings.Count(logs.String(), "analytics: buffer full"); n != 2 {
		t.Fatalf("%d overflow warnings after the interval, want 2", n)
	}
	if !strings.Contains(logs.String(), "dropped=11") {
		t.Errorf("second warning should report the 11 drops since the first:\n%s", logs.String())
	}
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, s); n != 3 {
		t.Fatalf("%d events stored, want 3", n)
	}
	if !b.Add(ev) {
		t.Fatal("add after flush dropped")
	}
}

func TestBufferFlushBatches(t *testing.T) {
	s, _ := openTestStore(t)
	txs := 0
	s.onTx = func() { txs++ }
	b := NewBuffer(s, 100, time.Hour, 4, nil)
	for i := 0; i < 10; i++ {
		b.Add(Event{TS: time.Now(), Kind: KindView, Path: "/"})
	}
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if txs != 3 {
		t.Fatalf("%d transactions for 10 events in batches of 4, want 3", txs)
	}
	if n := countEvents(t, s); n != 10 {
		t.Fatalf("%d events, want 10", n)
	}
}

func TestBufferFlushOnStop(t *testing.T) {
	s, _ := openTestStore(t)
	b := NewBuffer(s, 100, time.Hour, 500, nil) // the ticker never fires during the test
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	for i := 0; i < 7; i++ {
		b.Add(Event{TS: time.Now(), Kind: KindView, Path: "/"})
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if n := countEvents(t, s); n != 7 {
		t.Fatalf("%d events after stop, want 7", n)
	}
}

func TestBufferRunFlushesOnTick(t *testing.T) {
	s, _ := openTestStore(t)
	b := NewBuffer(s, 100, 20*time.Millisecond, 500, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	b.Add(Event{TS: time.Now(), Kind: KindView, Path: "/"})
	deadline := time.Now().Add(5 * time.Second)
	for countEvents(t, s) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("event not flushed by the ticker")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBufferDefaultsZeroTimestamp(t *testing.T) {
	s, _ := openTestStore(t)
	b := NewBuffer(s, 10, time.Hour, 500, nil)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return at }
	b.Add(Event{Kind: KindView, Path: "/"})
	b.now = func() time.Time { return at.Add(72 * time.Hour) }
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ts string
	if err := s.DB().QueryRow(`SELECT ts FROM events`).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts != "2026-11-03T09:00:00Z" {
		t.Fatalf("ts = %q, want the Add time", ts)
	}
}
