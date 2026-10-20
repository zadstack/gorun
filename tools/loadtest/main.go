// Command loadtest runs a practical HTTP load harness against a local GoRun
// SQLite instance (or an existing base URL). It reports p50/p95 latency, error
// rate, throughput, and approximate server RSS/CPU under N concurrent readers
// (optional light write mix).
//
//	make loadtest
//	go run ./tools/loadtest -concurrency 40 -duration 20s -write-mix 0.05
//
// See docs/LOADTEST.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	concurrency := fs.Int("concurrency", 25, "concurrent clients")
	duration := fs.Duration("duration", 15*time.Second, "steady-state duration")
	writeMix := fs.Float64("write-mix", 0, "fraction of requests that PATCH one seeded post (0..1)")
	posts := fs.Int("posts", 40, "published posts to seed before the run (local mode)")
	bin := fs.String("bin", "bin/gorun", "gorun binary (built if missing)")
	themes := fs.String("themes", "themes", "themes directory")
	dataDir := fs.String("data", "", "data directory (temp when empty; kept when set)")
	baseURL := fs.String("base-url", "", "use existing server instead of spawning local")
	token := fs.String("token", "", "API bearer token (required with -base-url for seed/writes)")
	serveEnv := fs.String("serve-env", "prod", "GORUN_ENV for the spawned serve process (prod|dev); prod caches themes")
	keepData := fs.Bool("keep-data", false, "keep temp data dir after local run")
	jsonOut := fs.Bool("json", false, "print report as JSON")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var (
		base      string
		apiToken  string
		samplePID int
		cleanup   func()
		notes     []string
	)

	if *baseURL != "" {
		base = strings.TrimRight(*baseURL, "/")
		apiToken = *token
		if *posts > 0 && apiToken == "" {
			fmt.Fprintln(os.Stderr, "loadtest: -token is required with -base-url when -posts > 0")
			return 2
		}
		if *writeMix > 0 && apiToken == "" {
			fmt.Fprintln(os.Stderr, "loadtest: -token is required when -write-mix > 0")
			return 2
		}
	} else {
		inst, err := StartLocal(ctx, *bin, *themes, *dataDir, *serveEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loadtest: start local: %v\n", err)
			return 1
		}
		base = inst.BaseURL
		apiToken = inst.Token
		samplePID = inst.PID()
		tempData := *dataDir == ""
		cleanup = func() {
			_ = inst.Close()
			if tempData && !*keepData {
				_ = os.RemoveAll(inst.DataDir)
			} else {
				notes = append(notes, "data dir: "+inst.DataDir)
			}
		}
		defer cleanup()
		fmt.Fprintf(os.Stderr, "loadtest: local server %s (pid %d, serve-env=%s, data %s)\n",
			base, samplePID, inst.Env, inst.DataDir)
	}

	seed, err := seedPublishedPosts(base, apiToken, *posts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadtest: seed: %v\n", err)
		return 1
	}

	reads := []Target{
		{Method: "GET", Path: "/"},
		{Method: "GET", Path: "/healthz"},
		{Method: "GET", Path: "/readyz"},
		{Method: "GET", Path: "/sitemap.xml"},
		{Method: "GET", Path: "/robots.txt"},
	}
	for _, p := range seed.Paths {
		reads = append(reads, Target{Method: "GET", Path: p})
	}

	writes := writeTargets(apiToken, seed.ItemID)
	if *writeMix > 0 && len(writes) == 0 {
		fmt.Fprintln(os.Stderr, "loadtest: write-mix requested but no write target (seed posts / token)")
		return 2
	}

	fmt.Fprintf(os.Stderr, "loadtest: concurrency=%d duration=%s write-mix=%.0f%% reads=%d\n",
		*concurrency, *duration, *writeMix*100, len(reads))

	rep, err := Run(ctx, RunnerConfig{
		BaseURL:     base,
		Concurrency: *concurrency,
		Duration:    *duration,
		WriteMix:    *writeMix,
		Timeout:     *timeout,
		Reads:       reads,
		Writes:      writes,
		SamplePID:   samplePID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadtest: run: %v\n", err)
		return 1
	}
	rep.Notes = append(rep.Notes, notes...)
	if samplePID == 0 && *baseURL != "" {
		rep.Notes = append(rep.Notes, "RSS/CPU not sampled in -base-url mode (no server pid)")
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		fmt.Print(rep.FormatText())
	}
	return 0
}
