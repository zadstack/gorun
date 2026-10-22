package admin_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/media"
)

type scheduledBackupTransport func(*http.Request) (*http.Response, error)

func (f scheduledBackupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestScheduledRemoteBackupPreservesLocalAndLastAcknowledgementOnFailure(t *testing.T) {
	s := boot(t)
	s.login("remote-schedule@example.invalid", "admin", "en")
	ctx := context.Background()
	dest, err := media.PrepareDestination("Private R2", "backup", media.S3Config{Endpoint: "https://objects.example.invalid", Bucket: "backups", AccessKey: "fixture-access", SecretKey: "fixture-secret"}, s.app.Deps.Config.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = media.SaveDestination(ctx, s.app.Deps.DB, s.app.Deps.Settings, dest); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"1"}, "remote_destination": {dest.ID}})
	if resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "backup.schedule.remote_destination", ""); got != dest.ID {
		t.Fatal("remote schedule not enabled")
	}
	calls, fail := 0, false
	client := &http.Client{Transport: scheduledBackupTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, ".gorb") || r.Header.Get("Authorization") == "" {
			t.Fatal("unexpected network operation", r.Method, r.URL.Path)
		}
		cipher, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := backup.Decrypt(bytes.NewReader(cipher), s.app.Deps.Config.SecretKey, strings.TrimSuffix(filepath.Base(r.URL.Path), ".gorb"))
		if err != nil || len(plain) == 0 {
			t.Fatal("uploaded copy cannot be decrypted", err)
		}
		status := 200
		if fail {
			status = 403
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("fixture-secret")), Header: http.Header{}}, nil
	})}
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err = admin.BackupScheduleWithClient(ctx, s.app.Deps, now, client); err != nil {
		t.Fatal(err)
	}
	var acknowledged string
	if err := s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key='storage.backup.last_success'`).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged == "" {
		t.Fatal("missing successful upload evidence")
	}
	fail = true
	if err = admin.BackupScheduleWithClient(ctx, s.app.Deps, now.Add(24*time.Hour), client); err == nil {
		t.Fatal("provider failure hidden")
	}
	if calls != 3 {
		t.Fatalf("unexpected number of uploads: %d", calls)
	}
	var got string
	if err := s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key='storage.backup.last_success'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != acknowledged {
		t.Fatal("failure overwrote last acknowledged upload")
	}
	entries, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(entries) != 2 {
		t.Fatal("failed upload pruned local recovery copies", entries, err)
	}
	if _, err = os.Stat(filepath.Join(backup.Dir(s.app.Deps.Config.DataDir), now.Add(24*time.Hour).Format("20060102T150405Z")+".db")); err != nil {
		t.Fatal(err)
	}
	var local struct{ Stamp string }
	if err = s.app.Deps.Settings.Get(ctx, admin.SettingBackupLastSuccess, &local); err != nil || local.Stamp != now.Add(24*time.Hour).Format("20060102T150405Z") {
		t.Fatal("remote failure erased local success", err)
	}
	_, page := s.do("GET", "/admin/system/backups", nil)
	mustContain(t, page, `data-schedule-status="warning"`, `data-remote-schedule-enabled="1"`, `data-remote-last-success`, "Private R2")
	mustNotContain(t, page, "fixture-secret", dest.Secret)
	_, page = s.do("GET", "/admin/system/health", nil)
	mustContain(t, page, `data-check="backups" data-status="warn"`)
	// The next tick must not create another local pair just to retry the upload.
	if err = admin.BackupScheduleWithClient(ctx, s.app.Deps, now.Add(25*time.Hour), client); err != nil || calls != 3 {
		t.Fatal("not-due tick uploaded again", err)
	}
}

func TestRemoteScheduleRequiresExplicitCurrentBackupDestination(t *testing.T) {
	for _, kind := range []string{"missing", "media", "changed"} {
		t.Run(kind, func(t *testing.T) {
			s := boot(t)
			s.login("schedule-policy@example.invalid", "admin", "en")
			ctx := context.Background()
			purpose := "backup"
			if kind == "media" {
				purpose = "media"
			}
			dest, err := media.PrepareDestination("Fixture", purpose, media.S3Config{Endpoint: "https://objects.example.invalid", Bucket: "one", AccessKey: "access", SecretKey: "secret"}, s.app.Deps.Config.SecretKey)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "missing" {
				if err = media.SaveDestination(ctx, s.app.Deps.DB, s.app.Deps.Settings, dest); err != nil {
					t.Fatal(err)
				}
			}
			selected := dest.ID
			if kind == "changed" {
				selected = strings.Repeat("a", 32)
			}
			s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"2"}, "remote_destination": {selected}})
			var hours int
			_ = s.app.Deps.Settings.Get(ctx, admin.SettingBackupScheduleHours, &hours)
			if hours != 0 || s.auditCount("system.backup_schedule", "policy") != 0 {
				t.Fatal("invalid destination partially saved schedule")
			}
		})
	}
}

func TestBackupScheduleAPIValidatesPolicyAndRedactsCredentials(t *testing.T) {
	s := boot(t)
	owner := s.createUser("backup-api@example.invalid", "admin", "en")
	editor := s.createUser("backup-editor@example.invalid", "editor", "en")
	path := "/api/v1/system/backups/schedule"
	code, _ := apiCall(t, s, editor, "GET", path, "")
	if code != 403 {
		t.Fatal("backup permission missing", code)
	}
	for _, body := range []string{`{}`, `{"hours":-1,"retain":1}`, `{"hours":24,"retain":101}`, `{"hours":24,"retain":1,"remote_destination":"unknown"}`, `{"hours":24,"retain":1,"token":"do-not-log-this"}`, `{"hours":24,"retain":1,"remote_destination":""} {}`, `{"hours":24,"retain":1,"remote_destination":""}` + strings.Repeat(" ", 4096)} {
		code, _ = apiCall(t, s, owner, "PUT", path, body)
		if code != 400 && code != 422 {
			t.Fatal("invalid policy accepted", body, code)
		}
	}
	code, out := apiCall(t, s, owner, "PUT", path, `{"hours":24,"retain":3,"remote_destination":""}`)
	if code != 200 {
		t.Fatal(code, out)
	}
	code, out = apiCall(t, s, owner, "GET", path, "")
	if code != 200 || out["data"].(map[string]any)["hours"] != float64(24) {
		t.Fatal("policy not readable", code, out)
	}
	if s.auditCount("system.backup_schedule", "policy") != 1 {
		t.Fatal("accepted policy not audited exactly once")
	}
}

func TestScheduledUploadDoesNotFollowChangedDestination(t *testing.T) {
	s := boot(t)
	s.login("changed-destination@example.invalid", "admin", "en")
	ctx := context.Background()
	config := media.S3Config{Endpoint: "https://objects.example.invalid", Bucket: "original", AccessKey: "access", SecretKey: "secret"}
	dest, err := media.PrepareDestination("Original", "backup", config, s.app.Deps.Config.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = media.SaveDestination(ctx, s.app.Deps.DB, s.app.Deps.Settings, dest); err != nil {
		t.Fatal(err)
	}
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"1"}, "remote_destination": {dest.ID}})
	config.Bucket = "replacement"
	replacement, err := media.PrepareDestination("Replacement", "backup", config, s.app.Deps.Config.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = media.SaveDestination(ctx, s.app.Deps.DB, s.app.Deps.Settings, replacement); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: scheduledBackupTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })}
	if err = admin.BackupScheduleWithClient(ctx, s.app.Deps, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), client); err == nil {
		t.Fatal("changed destination did not pause upload")
	}
	if calls != 0 {
		t.Fatal("uploaded without consent to changed destination")
	}
	entries, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("local backup lost", err)
	}
	// Saving without the checkbox is an explicit opt-out; local scheduling stays enabled.
	s.post("/admin/system/backups/schedule", url.Values{"schedule_hours": {"24"}, "retain": {"1"}})
	if err = admin.BackupScheduleWithClient(ctx, s.app.Deps, time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), client); err != nil || calls != 0 {
		t.Fatal("disabled upload still ran", err)
	}
}

func TestScheduledBackupsQueueOnceAndRespectDisabledPolicy(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	if err := admin.BackupScheduleTick(ctx, s.app.Deps); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='backup.scheduled'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled schedule queued", count, err)
	}
	if err := s.app.Deps.Settings.Set(ctx, admin.SettingBackupScheduleHours, 24); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := admin.BackupScheduleTick(ctx, s.app.Deps); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='backup.scheduled' AND status='queued' AND max_attempts=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate scheduled jobs", count, err)
	}
	if entries, err := backup.List(s.app.Deps.Config.DataDir); err != nil || len(entries) != 0 {
		t.Fatal("cron performed backup instead of enqueueing", err)
	}
}
