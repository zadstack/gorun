// Package metrics serves an optional Prometheus text exposition at GET /metrics.
// No Prometheus client library: counters and gauges are written by hand.
package metrics

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// ContentType is the Prometheus text 0.0.4 media type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// settingBackupScheduleLast matches admin.SettingBackupScheduleLast.
const settingBackupScheduleLast = "backup.schedule.last"

// HTTP holds process-lifetime request counters by status class.
type HTTP struct {
	c2xx, c3xx, c4xx, c5xx atomic.Uint64
}

// Middleware records one request per response by HTTP status class.
func (h *HTTP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		switch {
		case sw.status >= 500:
			h.c5xx.Add(1)
		case sw.status >= 400:
			h.c4xx.Add(1)
		case sw.status >= 300:
			h.c3xx.Add(1)
		default:
			// 1xx/2xx and handlers that never called WriteHeader (treated as 200).
			h.c2xx.Add(1)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush keeps http.Flusher available to streaming handlers (SSE, MCP).
func (s *statusWriter) Flush() {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

// Hijack keeps http.Hijacker available (e.g. WebSockets).
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

// Handler serves Prometheus text. token empty = open (network trust, like /healthz).
func Handler(token string, httpm *HTTP, q *jobs.Queue, st *settings.Store, dataDir string) http.HandlerFunc {
	if httpm == nil {
		httpm = &HTTP{}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && !authorized(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := render(r.Context(), httpm, q, st, dataDir)
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func authorized(r *http.Request, token string) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, prefix) && secretEq(auth[len(prefix):], token) {
		return true
	}
	return secretEq(r.Header.Get("X-Metrics-Token"), token)
}

func secretEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func render(ctx context.Context, httpm *HTTP, q *jobs.Queue, st *settings.Store, dataDir string) ([]byte, error) {
	var b strings.Builder
	writeHelpType(&b, "gorun_http_requests_total", "counter",
		"HTTP responses by status class since process start (includes /metrics scrapes).")
	for _, row := range []struct {
		class string
		n     uint64
	}{
		{"2xx", httpm.c2xx.Load()},
		{"3xx", httpm.c3xx.Load()},
		{"4xx", httpm.c4xx.Load()},
		{"5xx", httpm.c5xx.Load()},
	} {
		fmt.Fprintf(&b, "gorun_http_requests_total{code_class=%q} %s\n", row.class, strconv.FormatUint(row.n, 10))
	}

	var stats jobs.Stats
	if q != nil {
		var err error
		stats, err = q.Stats(ctx)
		if err != nil {
			return nil, err
		}
	}
	writeHelpType(&b, "gorun_jobs", "gauge",
		"Background jobs by status (failed = queued awaiting retry).")
	for _, row := range []struct {
		status string
		n      int
	}{
		{"queued", stats.Queued},
		{"running", stats.Running},
		{"failed", stats.Failed},
		{"dead", stats.Dead},
		{"done", stats.Done},
	} {
		fmt.Fprintf(&b, "gorun_jobs{status=%q} %d\n", row.status, row.n)
	}

	ts := backupLastSuccessUnix(ctx, st, dataDir)
	writeHelpType(&b, "gorun_backup_last_success_timestamp_seconds", "gauge",
		"Unix time of the newest successful local backup (schedule last or complete on-disk pair); 0 if none.")
	fmt.Fprintf(&b, "gorun_backup_last_success_timestamp_seconds %d\n", ts)
	return []byte(b.String()), nil
}

func writeHelpType(b *strings.Builder, name, typ, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func backupLastSuccessUnix(ctx context.Context, st *settings.Store, dataDir string) int64 {
	var best time.Time
	if st != nil {
		var s string
		if err := st.Get(ctx, settingBackupScheduleLast, &s); err == nil && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				best = t.UTC()
			}
		}
	}
	if dataDir != "" {
		entries, err := backup.List(dataDir)
		if err == nil {
			for _, e := range entries {
				if e.DBSize < 0 || e.FilesSize < 0 || e.Time.IsZero() {
					continue
				}
				if e.Time.After(best) {
					best = e.Time.UTC()
				}
			}
		}
	}
	if best.IsZero() {
		return 0
	}
	return best.Unix()
}
