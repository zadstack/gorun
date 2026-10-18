package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/jobs"
)

var queuedRe = regexp.MustCompile(`data-stat="queued">(\d+)<`)

func TestHealthReportsJobsBacklog(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	later := jobs.RunAt(time.Now().Add(time.Hour)) // keep the workers away
	var ids []string
	for i := 0; i < 3; i++ {
		id, err := s.app.Deps.Jobs.Enqueue(ctx, "test.backlog", map[string]int{"n": i}, later)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	db := s.app.Deps.DB
	if _, err := db.Exec(`UPDATE jobs SET status = 'dead', attempts = 5, last_error = 'boom dead' WHERE id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET attempts = 1, last_error = 'boom retry' WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	s.app.Deps.Hooks.Filter(admin.HealthCheckFilter, 10, func(_ context.Context, v any) (any, error) {
		list := v.(*[]admin.HealthCheck)
		*list = append(*list, admin.HealthCheck{Name: "billing", Status: admin.CheckFail, Detail: "unreachable"})
		return list, nil
	})

	resp, page := s.do("GET", "/admin/system/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	mustContain(t, page,
		`data-stat="failed">1<`, `data-stat="dead">1<`,
		`data-check="jobs" data-status="warn"`, `data-check="billing" data-status="fail"`,
		"boom dead", "boom retry", `action="/admin/system/jobs/`+ids[0]+`/retry"`,
		"journal_mode=wal", "auth", "admin", "go1.",
		`data-stat="gorun-version">`, "GitHub releases")
	mustNotContain(t, page, "secret")
	// Other jobs (e.g. the user.login event) may be queued too.
	if m := queuedRe.FindStringSubmatch(page); m == nil || m[1] == "0" {
		t.Fatalf("queued backlog not reported: %v", m)
	}

	resp, _ = s.post("/admin/system/jobs/"+ids[0]+"/retry", url.Values{"back": {"/admin/system/health"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/system/health" {
		t.Fatalf("retry: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if s.auditCount("jobs.retry", ids[0]) != 1 {
		t.Fatal("retry not audited")
	}
	resp, _ = s.post("/admin/system/jobs/"+ids[0]+"/retry", url.Values{"back": {"https://evil.test"}})
	if loc := resp.Header.Get("Location"); loc != "/admin/system/jobs" {
		t.Fatalf("open redirect via back: %q", loc)
	}
}

func TestJobsPageFiltersAndPreview(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	id, _ := s.app.Deps.Jobs.Enqueue(ctx, "test.preview", map[string]string{"hello": "world"}, jobs.RunAt(time.Now().Add(time.Hour)))
	s.app.Deps.DB.Exec(`UPDATE jobs SET status = 'dead' WHERE id = ?`, id)
	_, page := s.do("GET", "/admin/system/jobs?status=dead", nil)
	mustContain(t, page, "test.preview", `&#34;hello&#34;: &#34;world&#34;`, `/admin/system/jobs/`+id+`/retry`)
	retry, _ := s.app.Deps.Jobs.Enqueue(ctx, "test.retrying", nil, jobs.RunAt(time.Now().Add(time.Hour)))
	s.app.Deps.DB.Exec(`UPDATE jobs SET attempts = 2 WHERE id = ?`, retry)
	_, page = s.do("GET", "/admin/system/jobs?status=queued", nil)
	mustNotContain(t, page, "test.retrying")
	_, page = s.do("GET", "/admin/system/jobs?status=retrying", nil)
	mustContain(t, page, "test.retrying")
	_, page = s.do("GET", "/admin/system/jobs?status=failed", nil) // alias
	mustContain(t, page, "test.retrying")
	_, page = s.do("GET", "/admin/system/jobs?status=done", nil)
	mustNotContain(t, page, "test.preview")
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/system/jobs", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor jobs: %d", resp.StatusCode)
	}
}
