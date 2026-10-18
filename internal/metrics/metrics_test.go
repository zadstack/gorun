package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

func TestHandlerOpenAndToken(t *testing.T) {
	h := Handler("", &HTTP{}, nil, nil, t.TempDir())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("open: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"gorun_http_requests_total",
		"gorun_jobs{status=\"dead\"}",
		"gorun_backup_last_success_timestamp_seconds 0",
		"# TYPE gorun_jobs gauge",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("content-type: %s", ct)
	}

	secured := Handler("s3cret", &HTTP{}, nil, nil, t.TempDir())
	rec = httptest.NewRecorder()
	secured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	secured.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("X-Metrics-Token", "s3cret")
	rec = httptest.NewRecorder()
	secured.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("header token: %d", rec.Code)
	}
}

func TestHTTPMiddlewareCounts(t *testing.T) {
	httpm := &HTTP{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/gone":
			w.WriteHeader(http.StatusFound)
		case "/bad":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	h := httpm.Middleware(inner)
	for _, p := range []string{"/ok", "/gone", "/bad", "/boom"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	}
	if httpm.c2xx.Load() != 1 || httpm.c3xx.Load() != 1 || httpm.c4xx.Load() != 1 || httpm.c5xx.Load() != 1 {
		t.Fatalf("counts 2xx=%d 3xx=%d 4xx=%d 5xx=%d",
			httpm.c2xx.Load(), httpm.c3xx.Load(), httpm.c4xx.Load(), httpm.c5xx.Load())
	}
}

func TestRenderJobsAndBackup(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "jobs", jobs.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := q.Enqueue(ctx, "demo", map[string]string{"x": "1"}); err != nil {
		t.Fatal(err)
	}
	st := settings.New(d)
	when := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := st.Set(ctx, settingBackupScheduleLast, when.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	backups := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	stamp := "20261018T110000Z"
	for _, name := range []string{stamp + ".db", stamp + ".tar.gz"} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	httpm := &HTTP{}
	httpm.c2xx.Add(3)
	body, err := render(ctx, httpm, q, st, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `gorun_http_requests_total{code_class="2xx"} 3`) {
		t.Fatalf("http counter:\n%s", s)
	}
	if !strings.Contains(s, `gorun_jobs{status="queued"} 1`) {
		t.Fatalf("jobs:\n%s", s)
	}
	want := fmt.Sprintf("gorun_backup_last_success_timestamp_seconds %d", when.Unix())
	if !strings.Contains(s, want) {
		t.Fatalf("backup schedule last should win over older on-disk pair:\n%s", s)
	}
}
