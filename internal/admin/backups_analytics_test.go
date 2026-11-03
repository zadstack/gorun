package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

func writeAnalyticsDB(t *testing.T, dataDir string) {
	t.Helper()
	d, err := db.Open(filepath.Join(dataDir, backup.AnalyticsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
}

// Manual and scheduled backups carry analytics.db as <stamp>-analytics.db
// unless analytics.backup_exclude is set.
func TestAdminBackupsHonourAnalyticsExclude(t *testing.T) {
	s := boot(t)
	s.login("backup-analytics@example.com", "admin", "en")
	ctx := context.Background()
	dataDir := s.app.Deps.Config.DataDir
	writeAnalyticsDB(t, dataDir)

	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	list, err := backup.List(dataDir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize < 0 {
		t.Fatalf("manual backup without analytics: %+v %v", list, err)
	}
	if _, page := s.do("GET", "/admin/system/backups", nil); !strings.Contains(page, `href="/admin/system/backups/`+list[0].Stamp+`-analytics.db"`) {
		t.Fatal("backups page has no analytics download link")
	}
	if resp, body := s.do("GET", "/admin/system/backups/"+list[0].Stamp+"-analytics.db", nil); resp.StatusCode != http.StatusOK || len(body) < 16 || body[:15] != "SQLite format 3" {
		t.Fatalf("analytics download: %d", resp.StatusCode)
	}

	if err := s.app.Deps.Settings.Set(ctx, backup.SettingSkipAnalytics, true); err != nil {
		t.Fatal(err)
	}
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"5"}})
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, now); err != nil {
		t.Fatal(err)
	}
	list, err = backup.List(dataDir)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	for _, e := range list {
		if e.Stamp == now.Format("20060102T150405Z") && e.AnalyticsSize != -1 {
			t.Fatalf("scheduled backup ignored analytics.backup_exclude: %+v", e)
		}
	}
}
