package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// LatencyStats summarises request durations (nanoseconds).
type LatencyStats struct {
	Count int
	Mean  time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
	Min   time.Duration
}

// Report is the full load-run summary operators re-run and compare.
type Report struct {
	BaseURL       string        `json:"base_url"`
	Concurrency   int           `json:"concurrency"`
	Duration      time.Duration `json:"duration"`
	WriteMix      float64       `json:"write_mix"`
	Requests      int64         `json:"requests"`
	Errors        int64         `json:"errors"`
	ErrorRate     float64       `json:"error_rate"`
	ThroughputRPS float64       `json:"throughput_rps"`
	Latency       LatencyStats  `json:"latency"`
	RSS           ProcRange     `json:"rss_bytes,omitempty"`
	CPU           ProcRange     `json:"cpu_percent,omitempty"`
	ReadPaths     []string      `json:"read_paths"`
	Notes         []string      `json:"notes,omitempty"`
}

// ProcRange is min/avg/max of a sampled process metric.
type ProcRange struct {
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	Max float64 `json:"max"`
	N   int     `json:"samples"`
}

// ComputeLatency sorts a copy of durations and returns percentiles.
func ComputeLatency(durs []time.Duration) LatencyStats {
	n := len(durs)
	if n == 0 {
		return LatencyStats{}
	}
	sorted := append([]time.Duration(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}
	return LatencyStats{
		Count: n,
		Mean:  sum / time.Duration(n),
		P50:   percentile(sorted, 0.50),
		P95:   percentile(sorted, 0.95),
		P99:   percentile(sorted, 0.99),
		Max:   sorted[n-1],
		Min:   sorted[0],
	}
}

// percentile uses the nearest-rank method on a non-empty sorted slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 1 {
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func summarizeSamples(vals []float64) ProcRange {
	if len(vals) == 0 {
		return ProcRange{}
	}
	min, max, sum := vals[0], vals[0], 0.0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	return ProcRange{Min: min, Avg: sum / float64(len(vals)), Max: max, N: len(vals)}
}

// FormatText renders a human-readable report for operators.
func (r Report) FormatText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "GoRun load harness\n")
	fmt.Fprintf(&b, "  base_url:     %s\n", r.BaseURL)
	fmt.Fprintf(&b, "  concurrency:  %d\n", r.Concurrency)
	fmt.Fprintf(&b, "  duration:     %s\n", r.Duration.Round(time.Millisecond))
	fmt.Fprintf(&b, "  write_mix:    %.0f%%\n", r.WriteMix*100)
	fmt.Fprintf(&b, "  requests:     %d\n", r.Requests)
	fmt.Fprintf(&b, "  errors:       %d (%.2f%%)\n", r.Errors, r.ErrorRate*100)
	fmt.Fprintf(&b, "  throughput:   %.1f req/s\n", r.ThroughputRPS)
	fmt.Fprintf(&b, "  latency:\n")
	fmt.Fprintf(&b, "    p50:  %s\n", r.Latency.P50)
	fmt.Fprintf(&b, "    p95:  %s\n", r.Latency.P95)
	fmt.Fprintf(&b, "    p99:  %s\n", r.Latency.P99)
	fmt.Fprintf(&b, "    mean: %s\n", r.Latency.Mean)
	fmt.Fprintf(&b, "    max:  %s\n", r.Latency.Max)
	if r.RSS.N > 0 {
		fmt.Fprintf(&b, "  rss (server process):\n")
		fmt.Fprintf(&b, "    min:  %s\n", formatBytes(r.RSS.Min))
		fmt.Fprintf(&b, "    avg:  %s\n", formatBytes(r.RSS.Avg))
		fmt.Fprintf(&b, "    max:  %s\n", formatBytes(r.RSS.Max))
	}
	if r.CPU.N > 0 {
		fmt.Fprintf(&b, "  cpu %% (server process, approx):\n")
		fmt.Fprintf(&b, "    min:  %.1f\n", r.CPU.Min)
		fmt.Fprintf(&b, "    avg:  %.1f\n", r.CPU.Avg)
		fmt.Fprintf(&b, "    max:  %.1f\n", r.CPU.Max)
	}
	if len(r.ReadPaths) > 0 {
		fmt.Fprintf(&b, "  read paths (%d): %s\n", len(r.ReadPaths), strings.Join(clip(r.ReadPaths, 8), ", "))
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "  note: %s\n", n)
	}
	return b.String()
}

func formatBytes(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GiB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KiB", b/(1<<10))
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func clip(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	out := append([]string{}, ss[:n]...)
	out = append(out, fmt.Sprintf("…(+%d)", len(ss)-n))
	return out
}
