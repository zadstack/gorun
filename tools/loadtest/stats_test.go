package main

import (
	"strings"
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	durs := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
		6 * time.Millisecond,
		7 * time.Millisecond,
		8 * time.Millisecond,
		9 * time.Millisecond,
		10 * time.Millisecond,
	}
	lat := ComputeLatency(durs)
	if lat.Count != 10 || lat.Min != time.Millisecond || lat.Max != 10*time.Millisecond {
		t.Fatalf("bounds: %+v", lat)
	}
	if lat.P50 != 5*time.Millisecond {
		t.Fatalf("p50=%s want 5ms", lat.P50)
	}
	if lat.P95 != 10*time.Millisecond {
		t.Fatalf("p95=%s want 10ms", lat.P95)
	}
	if lat.Mean != (1+2+3+4+5+6+7+8+9+10)*time.Millisecond/10 {
		t.Fatalf("mean=%s", lat.Mean)
	}
}

func TestComputeLatencyEmptyAndSingle(t *testing.T) {
	if ComputeLatency(nil).Count != 0 {
		t.Fatal("empty")
	}
	lat := ComputeLatency([]time.Duration{42 * time.Millisecond})
	if lat.P50 != 42*time.Millisecond || lat.P95 != 42*time.Millisecond || lat.P99 != 42*time.Millisecond {
		t.Fatalf("%+v", lat)
	}
}

func TestComputeLatencyDoesNotMutateInput(t *testing.T) {
	in := []time.Duration{3 * time.Millisecond, 1 * time.Millisecond, 2 * time.Millisecond}
	_ = ComputeLatency(in)
	if in[0] != 3*time.Millisecond || in[1] != time.Millisecond {
		t.Fatalf("input mutated: %v", in)
	}
}

func TestSummarizeSamples(t *testing.T) {
	r := summarizeSamples([]float64{10, 20, 30})
	if r.N != 3 || r.Min != 10 || r.Max != 30 || r.Avg != 20 {
		t.Fatalf("%+v", r)
	}
	if summarizeSamples(nil).N != 0 {
		t.Fatal("empty")
	}
}

func TestReportFormatText(t *testing.T) {
	r := Report{
		BaseURL:       "http://127.0.0.1:9",
		Concurrency:   10,
		Duration:      2 * time.Second,
		Requests:      100,
		Errors:        1,
		ErrorRate:     0.01,
		ThroughputRPS: 50,
		Latency:       LatencyStats{P50: time.Millisecond, P95: 2 * time.Millisecond, P99: 3 * time.Millisecond, Mean: time.Millisecond, Max: 4 * time.Millisecond},
		RSS:           ProcRange{Min: 10 << 20, Avg: 12 << 20, Max: 14 << 20, N: 3},
		CPU:           ProcRange{Min: 5, Avg: 10, Max: 20, N: 3},
		ReadPaths:     []string{"/", "/healthz"},
		Notes:         []string{"fixture"},
	}
	s := r.FormatText()
	for _, want := range []string{"p50:", "p95:", "rss", "cpu", "fixture", "50.0 req/s"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	if formatBytes(512) != "512 B" {
		t.Fatal(formatBytes(512))
	}
	if !strings.Contains(formatBytes(5<<20), "MiB") {
		t.Fatal(formatBytes(5 << 20))
	}
}
