package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/db"
)

// I7: the jobs.prune cron honours the jobs.retain_* settings (injected
// clock), and System Health shows the job table row count.
func TestJobsPruneCronAndHealthRowCount(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(id, status string, age time.Duration) {
		t.Helper()
		ts := db.FormatTime(now.Add(-age))
		if _, err := s.app.Deps.DB.Exec(`INSERT INTO jobs (id, kind, status, run_at, created_at, updated_at) VALUES (?, 'x', ?, ?, ?, ?)`,
			id, status, ts, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	add("p-done-3d", "done", 3*day)
	add("p-done-10d", "done", 10*day)
	add("p-dead-10d", "dead", 10*day)

	if err := admin.PruneJobsAt(ctx, s.app.Deps, now); err != nil {
		t.Fatal(err)
	}
	exists := func(id string) bool {
		var n int
		_ = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE id = ?`, id).Scan(&n)
		return n == 1
	}
	if !exists("p-done-3d") || exists("p-done-10d") || !exists("p-dead-10d") {
		t.Fatal("default retention: done 7d, dead 30d")
	}
	// Shorter windows from settings.
	if err := s.app.Deps.Settings.Set(ctx, admin.SettingRetainDoneDays, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.app.Deps.Settings.Set(ctx, admin.SettingRetainDeadDays, 5); err != nil {
		t.Fatal(err)
	}
	if err := admin.PruneJobsAt(ctx, s.app.Deps, now); err != nil {
		t.Fatal(err)
	}
	if exists("p-done-3d") || exists("p-dead-10d") {
		t.Fatal("settings retention not applied")
	}

	_, page := s.do("GET", "/admin/system/health", nil)
	if !strings.Contains(page, `data-stat="job-rows">`) || !strings.Contains(page, "Job table rows") {
		t.Fatalf("health page lacks the job row count:\n%s", page)
	}
	admin2 := s.createUser("api@example.com", "admin", "en")
	_, out := apiCall(t, s, admin2, "GET", "/api/v1/system/health", "")
	if _, ok := out["data"].(map[string]any)["job_rows"].(float64); !ok {
		t.Fatalf("job_rows missing: %v", out["data"])
	}
}
