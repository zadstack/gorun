package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/backup"
)

func TestBackupScheduleSaveAndCronRetain(t *testing.T) {
	s := boot(t)
	s.login("backup-sched@example.invalid", "admin", "en")
	ctx := context.Background()

	resp, page := s.do("GET", "/admin/system/backups", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `data-backup-schedule`) || !strings.Contains(page, `data-schedule-enabled="0"`) {
		t.Fatalf("schedule form missing:\n%s", page)
	}
	if resp, _ = s.post("/admin/system/backups/schedule", url.Values{
		"schedule_hours": {"24"},
		"retain":         {"2"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	if s.auditCount("system.backup_schedule", "policy") != 1 {
		t.Fatal("schedule save not audited")
	}
	resp, page = s.do("GET", "/admin/system/backups", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `data-schedule-enabled="1"`) {
		t.Fatalf("schedule not enabled on page:\n%s", page)
	}

	t0 := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, t0); err != nil {
		t.Fatal(err)
	}
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 1 {
		t.Fatalf("first due create: list=%d err=%v", len(list), err)
	}
	if s.auditCount("system.backup_scheduled", list[0].Stamp) != 1 {
		t.Fatal("scheduled create not audited")
	}

	t1 := t0.Add(24 * time.Hour)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, t1); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(24 * time.Hour)
	if err := admin.BackupScheduleAt(ctx, s.app.Deps, t2); err != nil {
		t.Fatal(err)
	}
	list, err = backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 2 {
		t.Fatalf("retain 2: list=%d err=%v", len(list), err)
	}
	if list[0].Stamp != t2.Format("20060102T150405Z") || list[1].Stamp != t1.Format("20060102T150405Z") {
		t.Fatalf("unexpected stamps: %+v", list)
	}
}

func TestBackupScheduleInvalidForm(t *testing.T) {
	s := boot(t)
	s.login("backup-bad@example.invalid", "admin", "en")
	if resp, _ := s.post("/admin/system/backups/schedule", url.Values{
		"schedule_hours": {"999"},
		"retain":         {"2"},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	var hours int
	_ = s.app.Deps.Settings.Get(context.Background(), admin.SettingBackupScheduleHours, &hours)
	if hours != 0 {
		t.Fatalf("invalid hours saved: %d", hours)
	}
}
