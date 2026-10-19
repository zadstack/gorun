package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Target is one HTTP request the harness can issue.
type Target struct {
	Method string
	Path   string
	Body   []byte
	Header http.Header
}

// RunnerConfig configures a load run against a base URL.
type RunnerConfig struct {
	BaseURL     string
	Concurrency int
	Duration    time.Duration
	WriteMix    float64 // 0..1 fraction of requests that pick from WriteTargets
	Timeout     time.Duration
	Reads       []Target
	Writes      []Target
	// SamplePID, when > 0, samples RSS/CPU for that process during the run.
	SamplePID   int
	SampleEvery time.Duration
}

// Run executes the load and returns a report. It is safe for unit tests with
// httptest.Server (pass BaseURL = srv.URL, SamplePID = 0).
func Run(ctx context.Context, cfg RunnerConfig) (Report, error) {
	if cfg.Concurrency < 1 {
		return Report{}, fmt.Errorf("concurrency must be >= 1")
	}
	if cfg.Duration <= 0 {
		return Report{}, fmt.Errorf("duration must be > 0")
	}
	if len(cfg.Reads) == 0 {
		return Report{}, fmt.Errorf("at least one read target is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.SampleEvery <= 0 {
		cfg.SampleEvery = 200 * time.Millisecond
	}
	if cfg.WriteMix < 0 || cfg.WriteMix > 1 {
		return Report{}, fmt.Errorf("write_mix must be between 0 and 1")
	}
	if cfg.WriteMix > 0 && len(cfg.Writes) == 0 {
		return Report{}, fmt.Errorf("write_mix > 0 requires write targets")
	}

	client := &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	var (
		reqs  atomic.Int64
		fails atomic.Int64
		mu    sync.Mutex
		durs  []time.Duration
	)

	var rssSamples, cpuSamples []float64
	var sampleStop chan struct{}
	if cfg.SamplePID > 0 && processSamplerAvailable() {
		sampleStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(cfg.SampleEvery)
			defer ticker.Stop()
			var prevCPU float64
			var prevAt time.Time
			first := true
			for {
				select {
				case <-sampleStop:
					return
				case <-runCtx.Done():
					return
				case now := <-ticker.C:
					rss, cpuTime, err := sampleProcess(cfg.SamplePID)
					if err != nil {
						continue
					}
					mu.Lock()
					rssSamples = append(rssSamples, float64(rss))
					if !first {
						elapsed := now.Sub(prevAt).Seconds()
						if elapsed > 0 {
							pct := 100 * (cpuTime - prevCPU) / elapsed
							if pct < 0 {
								pct = 0
							}
							cpuSamples = append(cpuSamples, pct)
						}
					}
					mu.Unlock()
					prevCPU, prevAt, first = cpuTime, now, false
				}
			}
		}()
	}

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(worker)))
			for {
				if runCtx.Err() != nil {
					return
				}
				t := pickTarget(rng, cfg)
				d, err := doRequest(runCtx, client, cfg.BaseURL, t)
				if err != nil {
					// End-of-run cancellation is not a server failure.
					if runCtx.Err() != nil || isContextErr(err) {
						return
					}
					reqs.Add(1)
					fails.Add(1)
					continue
				}
				reqs.Add(1)
				mu.Lock()
				durs = append(durs, d)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if sampleStop != nil {
		close(sampleStop)
	}

	mu.Lock()
	lat := ComputeLatency(durs)
	rss := summarizeSamples(rssSamples)
	cpu := summarizeSamples(cpuSamples)
	mu.Unlock()

	total := reqs.Load()
	errN := fails.Load()
	var errRate float64
	if total > 0 {
		errRate = float64(errN) / float64(total)
	}
	var rps float64
	if elapsed > 0 {
		rps = float64(total) / elapsed.Seconds()
	}

	paths := make([]string, 0, len(cfg.Reads))
	for _, t := range cfg.Reads {
		paths = append(paths, t.Path)
	}

	rep := Report{
		BaseURL:       cfg.BaseURL,
		Concurrency:   cfg.Concurrency,
		Duration:      cfg.Duration,
		WriteMix:      cfg.WriteMix,
		Requests:      total,
		Errors:        errN,
		ErrorRate:     errRate,
		ThroughputRPS: rps,
		Latency:       lat,
		RSS:           rss,
		CPU:           cpu,
		ReadPaths:     paths,
	}
	if cfg.SamplePID > 0 && !processSamplerAvailable() {
		rep.Notes = append(rep.Notes, "RSS/CPU sampling unsupported on this OS; latency still measured")
	}
	return rep, nil
}

func pickTarget(rng *rand.Rand, cfg RunnerConfig) Target {
	if cfg.WriteMix > 0 && len(cfg.Writes) > 0 && rng.Float64() < cfg.WriteMix {
		return cfg.Writes[rng.Intn(len(cfg.Writes))]
	}
	return cfg.Reads[rng.Intn(len(cfg.Reads))]
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(err != nil && (strings.Contains(err.Error(), "context canceled") ||
			strings.Contains(err.Error(), "context deadline exceeded")))
}

func doRequest(ctx context.Context, client *http.Client, base string, t Target) (time.Duration, error) {
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(t.Body) > 0 {
		body = bytes.NewReader(t.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+t.Path, body)
	if err != nil {
		return 0, err
	}
	for k, vs := range t.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if len(t.Body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := client.Do(req)
	d := time.Since(start)
	if err != nil {
		return d, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return d, fmt.Errorf("status %d", resp.StatusCode)
	}
	return d, nil
}

// seedResult holds public read paths and one item id for light write PATCHes.
type seedResult struct {
	Paths  []string
	ItemID string
}

// seedPublishedPosts creates N published posts via the content API.
func seedPublishedPosts(base, token string, n int) (seedResult, error) {
	var out seedResult
	if n < 1 {
		return out, nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("loadtest-post-%04d", i+1)
		body, _ := json.Marshal(map[string]any{
			"type":   "post",
			"title":  fmt.Sprintf("Load test post %d", i+1),
			"slug":   slug,
			"status": "published",
			"body":   fmt.Sprintf("<p>Synthetic load-test body %d.</p>", i+1),
		})
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/content", bytes.NewReader(body))
		if err != nil {
			return out, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return out, err
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return out, fmt.Errorf("seed post %s: %d %.200s", slug, resp.StatusCode, b)
		}
		var env struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(b, &env)
		if out.ItemID == "" && env.Data.ID != "" {
			out.ItemID = env.Data.ID
		}
		out.Paths = append(out.Paths, "/post/"+slug)
	}
	return out, nil
}

func writeTargets(token, itemID string) []Target {
	if token == "" || itemID == "" {
		return nil
	}
	// Light write: PATCH body of one seeded post (does not grow the DB).
	body, _ := json.Marshal(map[string]any{
		"body": fmt.Sprintf("<p>write-mix %d</p>", time.Now().UnixNano()),
	})
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	return []Target{{
		Method: http.MethodPatch,
		Path:   "/api/v1/content/" + itemID,
		Body:   body,
		Header: h,
	}}
}
