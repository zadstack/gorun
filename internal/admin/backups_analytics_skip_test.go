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

func corruptAnalyticsDB(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, backup.AnalyticsFile), []byte(strings.Repeat("garbage ", 600)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A corrupt analytics.db never stops a scheduled backup: the verified core
// pair is recorded and the skip is a saved, audited warning.
func TestScheduledBackupWarnsWhenAnalyticsSkipped(t *testing.T) {
	s := boot(t)
	s.login("backup-analytics-skip@example.com", "admin", "en")
	ctx := context.Background()
	corruptAnalyticsDB(t, s.app.Deps.Config.DataDir)
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"5"}})
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	err := admin.BackupScheduleAt(ctx, s.app.Deps, now)
	if err != nil && !strings.Contains(err.Error(), "warning") {
		t.Fatalf("scheduled backup failed: %v", err)
	}
	var state struct{ Status, Stage, Stamp string }
	if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleState, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "warning" || state.Stage != "analytics" {
		t.Fatalf("state: %+v", state)
	}
	var last string
	if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleLast, &last); err != nil || last != now.Format(time.RFC3339) {
		t.Fatalf("verified success not recorded: %q %v", last, err)
	}
	if s.auditCount("system.backup_analytics_skipped", state.Stamp) != 1 || s.auditCount("system.backup_schedule_warning", state.Stamp) != 1 {
		t.Fatal("skip not audited")
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize != -1 || list[0].LocalState != "paired_not_inspected" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, page := s.do("GET", "/admin/system/backups", nil); !strings.Contains(page, `data-schedule-status="warning"`) || !strings.Contains(page, "analytics.db was left out") {
		t.Fatal("warning not shown on the Backups page")
	}
}

func TestManualBackupWarnsWhenAnalyticsSkipped(t *testing.T) {
	s := boot(t)
	s.login("backup-analytics-manual@example.com", "admin", "en")
	corruptAnalyticsDB(t, s.app.Deps.Config.DataDir)
	resp, _ := s.post("/admin/system/backups", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	flash := ""
	for _, c := range resp.Cookies() {
		if v, err := url.QueryUnescape(c.Value); err == nil && strings.Contains(v, "|admin.backups.") {
			flash = v
		}
	}
	if flash != "info|admin.backups.analytics_skipped" {
		t.Fatalf("flash = %q", flash)
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize != -1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if s.auditCount("system.backup", list[0].Stamp) != 1 || s.auditCount("system.backup_analytics_skipped", list[0].Stamp) != 1 {
		t.Fatal("audit")
	}
}

// An unreadable exclusion setting fails closed: analytics is left out, with
// a warning, rather than copied against the operator's choice.
func TestBackupExclusionSettingFailsClosed(t *testing.T) {
	s := boot(t)
	s.login("backup-analytics-closed@example.com", "admin", "en")
	writeAnalyticsDB(t, s.app.Deps.Config.DataDir)
	if err := s.app.Deps.Settings.Set(context.Background(), backup.SettingSkipAnalytics, "yes"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 1 || list[0].AnalyticsSize != -1 {
		t.Fatalf("analytics copied although the setting is unreadable: %+v %v", list, err)
	}
	if s.auditCount("system.backup_analytics_skipped", list[0].Stamp) != 1 {
		t.Fatal("fail-closed skip not reported")
	}
}

// The pre-update backup honours the exclusion, still passes when analytics
// is broken (recording the skip), and counts the analytics copy in bytes.
func TestUpdateBackupAnalytics(t *testing.T) {
	ctx := context.Background()
	t.Run("included and counted", func(t *testing.T) {
		s := boot(t)
		writeAnalyticsDB(t, s.app.Deps.Config.DataDir)
		rep, err := admin.UpdateBackup(ctx, s.app.Deps)
		if err != nil || !rep.OK || rep.Data["analytics"] != true {
			t.Fatalf("%+v %v", rep, err)
		}
		m, err := backup.ReadManifest(filepath.Join(backup.Dir(s.app.Deps.Config.DataDir), rep.Data["stamp"].(string)+".manifest.json"))
		if err != nil || m.Analytics == nil {
			t.Fatalf("%+v %v", m, err)
		}
		if rep.Data["bytes"] != want(m) {
			t.Fatalf("bytes = %v, want %d", rep.Data["bytes"], want(m))
		}
		var success struct{ Bytes int64 }
		if err := s.app.Deps.Settings.Get(ctx, admin.SettingBackupLastSuccess, &success); err != nil || success.Bytes != want(m) {
			t.Fatalf("last success bytes %d: %v", success.Bytes, err)
		}
	})
	t.Run("excluded", func(t *testing.T) {
		s := boot(t)
		writeAnalyticsDB(t, s.app.Deps.Config.DataDir)
		if err := s.app.Deps.Settings.Set(ctx, backup.SettingSkipAnalytics, true); err != nil {
			t.Fatal(err)
		}
		rep, err := admin.UpdateBackup(ctx, s.app.Deps)
		if err != nil || !rep.OK || rep.Data["analytics"] != false || rep.Data["analytics_skipped"] != nil {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("broken still passes", func(t *testing.T) {
		s := boot(t)
		corruptAnalyticsDB(t, s.app.Deps.Config.DataDir)
		rep, err := admin.UpdateBackup(ctx, s.app.Deps)
		if err != nil || !rep.OK {
			t.Fatalf("update blocked by analytics: %+v %v", rep, err)
		}
		if skipped, _ := rep.Data["analytics_skipped"].(string); !strings.HasPrefix(skipped, backup.AnalyticsCopyFailed) {
			t.Fatalf("skip not recorded: %+v", rep.Data)
		}
	})
}

func want(m *backup.Manifest) int64 {
	return m.Database.Size + m.Archive.Size + m.Analytics.Size
}
