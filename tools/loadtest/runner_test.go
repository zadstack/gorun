package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunReportsLatencyAndErrors(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/fail":
			http.Error(w, "nope", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	rep, err := Run(context.Background(), RunnerConfig{
		BaseURL:     srv.URL,
		Concurrency: 4,
		Duration:    300 * time.Millisecond,
		Reads: []Target{
			{Method: "GET", Path: "/ok"},
			{Method: "GET", Path: "/fail"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Requests < 1 || hits.Load() < 1 {
		t.Fatalf("no requests: %+v hits=%d", rep, hits.Load())
	}
	if rep.Errors == 0 {
		t.Fatal("expected some errors from /fail")
	}
	if rep.Latency.Count == 0 {
		t.Fatal("expected successful latency samples from /ok")
	}
	if rep.ErrorRate <= 0 || rep.ErrorRate >= 1 {
		t.Fatalf("error rate odd: %v", rep.ErrorRate)
	}
	if rep.ThroughputRPS <= 0 {
		t.Fatalf("rps=%v", rep.ThroughputRPS)
	}
}

func TestRunWriteMix(t *testing.T) {
	var writes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	rep, err := Run(context.Background(), RunnerConfig{
		BaseURL:     srv.URL,
		Concurrency: 6,
		Duration:    400 * time.Millisecond,
		WriteMix:    0.5,
		Reads:       []Target{{Method: "GET", Path: "/r"}},
		Writes:      []Target{{Method: "PATCH", Path: "/w", Body: []byte(`{"body":"x"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() == 0 {
		t.Fatalf("expected write mix traffic; report=%+v", rep)
	}
}

func TestRunValidation(t *testing.T) {
	_, err := Run(context.Background(), RunnerConfig{Concurrency: 0, Duration: time.Second, Reads: []Target{{Path: "/"}}})
	if err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("got %v", err)
	}
	_, err = Run(context.Background(), RunnerConfig{Concurrency: 1, Duration: time.Second, WriteMix: 0.1, Reads: []Target{{Path: "/"}}})
	if err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteTargetsRequiresID(t *testing.T) {
	if writeTargets("tok", "") != nil {
		t.Fatal("expected nil")
	}
	ts := writeTargets("tok", "ABC")
	if len(ts) != 1 || ts[0].Method != http.MethodPatch || !strings.Contains(ts[0].Path, "ABC") {
		t.Fatalf("%+v", ts)
	}
}
