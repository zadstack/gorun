package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/backup"
)

func TestScheduledBackupFailureIsVisibleAndKeepsLastSuccess(t *testing.T) {
	s := boot(t)
	s.login("backup-owner@example.com", "admin", "en")
	ctx := context.Background()
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"2"}})
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, now); err != nil {
		t.Fatal(err)
	}
	// A pre-existing reserved destination makes the next scheduled creation fail.
	target := filepath.Join(backup.Dir(s.app.Deps.Config.DataDir), now.Add(24*time.Hour).Format("20060102T150405Z")+".db")
	if err := os.WriteFile(target, []byte("leave this file alone"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, now.Add(24*time.Hour)); err == nil {
		t.Fatal("expected scheduled failure")
	}
	var last string
	if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleLast, &last); err != nil || last != now.Format(time.RFC3339) {
		t.Fatalf("last successful backup changed: %s %v", last, err)
	}
	resp, page := s.do("GET", "/admin/system/backups", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `data-schedule-status="failed"`) || !strings.Contains(page, `data-backup-last-success`) {
		t.Fatal("failure/last success missing from backup page")
	}
	resp, page = s.do("GET", "/admin/system/health", nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `data-check="backups" data-status="fail"`) {
		t.Fatal("scheduled failure not reported in system health")
	}
	if s.auditCount("system.backup_schedule_failed", "schedule") != 1 {
		t.Fatal("scheduled failure not audited")
	}
}

func TestBackupSchedulePolicySaveIsAtomic(t *testing.T) {
	s := boot(t)
	s.login("policy-owner@example.com", "admin", "en")
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"7"}})
	if _, err := s.app.Deps.DB.Exec(`CREATE TRIGGER reject_backup_policy BEFORE UPDATE ON settings WHEN NEW.key = 'backup.retain' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"168"}, "retain": {"3"}})
	if resp.StatusCode != 500 {
		t.Fatalf("save status %d", resp.StatusCode)
	}
	var hours int
	if err := s.app.Deps.Settings.Get(context.Background(), admin.SettingBackupScheduleHours, &hours); err != nil || hours != 24 {
		t.Fatal("failed save partially changed schedule")
	}
}

func TestBackupPageShowsDailyWeeklyAndStorage(t *testing.T) {
	s := boot(t)
	s.login("backup-ui@example.com", "admin", "en")
	_, page := s.do("GET", "/admin/system/backups", nil)
	for _, want := range []string{`option value="24"`, `option value="168"`, `data-backup-storage`, `data-backup-last-success`} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != 303 {
		t.Fatal("manual backup failed")
	}
	var success struct{ At, Stamp string }
	if err := s.app.Deps.Settings.Get(context.Background(), admin.SettingBackupLastSuccess, &success); err != nil || success.At == "" || success.Stamp == "" {
		t.Fatal("manual verified success not recorded")
	}
	_, page = s.do("GET", "/admin/system/backups/"+success.Stamp+"/inspect", nil)
	if !strings.Contains(page, `data-backup-preview`) || !strings.Contains(page, "gorun backup-preview --input") {
		t.Fatal("safe preview instructions missing")
	}

}

func TestUnverifiedScheduledCopyNeverPrunesPreviousGoodBackup(t *testing.T) {
	s := boot(t)
	s.login("integrity-owner@example.com", "admin", "en")
	ctx := context.Background()
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"1"}})
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, now); err != nil {
		t.Fatal(err)
	}
	good, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(good) != 1 {
		t.Fatal(err)
	}
	// A real SQLite foreign-key violation is sufficient to fail verification even
	// though VACUUM and both archive checksums complete successfully.
	conn, err := s.app.Deps.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`PRAGMA foreign_keys=OFF`, `CREATE TABLE backup_test_parent (id TEXT PRIMARY KEY)`, `CREATE TABLE backup_test_child (parent_id TEXT REFERENCES backup_test_parent(id))`, `INSERT INTO backup_test_child VALUES ('missing')`, `PRAGMA foreign_keys=ON`} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	conn.Close()
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, now.Add(24*time.Hour)); err == nil {
		t.Fatal("invalid snapshot marked successful")
	}
	if _, err := os.Stat(filepath.Join(backup.Dir(s.app.Deps.Config.DataDir), good[0].Stamp+".db")); err != nil {
		t.Fatal("last good backup was pruned")
	}
	var state struct{ Status, Stage string }
	if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleState, &state); err != nil || state.Status != "failed" || state.Stage != "verify" {
		t.Fatalf("verification failure not persisted: %+v %v", state, err)
	}
	var last string
	if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleLast, &last); err != nil || last != now.Format(time.RFC3339) {
		t.Fatal("failed verification advanced last success")
	}
}
