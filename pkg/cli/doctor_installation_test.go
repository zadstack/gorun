package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/update"
)

func requireDoctorStatus(t *testing.T, checks []siteDoctorCheck, id, status string) siteDoctorCheck {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			if check.Status != status || check.Message == "" || (status != "ok" && check.Action == "") {
				t.Fatalf("%s: got %+v, want %s with a message and a fix when applicable", id, check, status)
			}
			return check
		}
	}
	t.Fatalf("%s missing from %+v", id, checks)
	return siteDoctorCheck{}
}

func TestDoctorInstallationAndSupport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_TRIAL", "")
	t.Setenv("GORUN_BASE_URL", "https://private.example")
	t.Setenv("GORUN_TRUSTED_PROXIES", "172.21.0.4")
	t.Setenv("GORUN_THEMES", filepath.Join(dir, "private@example.com"))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{13}, 32))
	t.Setenv("GORUN_SECRET_KEY", key)
	var out bytes.Buffer
	err := doctorCmd([]string{"--json", "--support"}, &out)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	for _, private := range []string{dir, key, "private.example", "private@example.com", "172.21.0.4"} {
		if strings.Contains(out.String(), private) {
			t.Fatal("support output leaked a configured value")
		}
	}
	var report siteDoctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"config", "version", "platform", "data", "permissions", "data_lock", "secret_key", "public_base_url", "trusted_proxies", "https_security"} {
		requireDoctorStatus(t, report.Checks, id, "ok")
	}
	for _, id := range []string{"themes_dir", "database"} {
		requireDoctorStatus(t, report.Checks, id, "fail")
	}
	requireDoctorStatus(t, report.Checks, "backup_age", "warn")
	for _, check := range report.Checks {
		if check.ID == "updates" {
			t.Fatal("update check ran by default")
		}
	}
	for _, name := range []string{"gorun.lock", "gorun.db", "secret.key", "backups"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created or is inaccessible: %v", name, err)
		}
	}

	t.Setenv("GORUN_SECRET_KEY", "c2hvcnQ=")
	t.Setenv("GORUN_TRUSTED_PROXIES", "0.0.0.0/0")
	t.Setenv("GORUN_HSTS_PRELOAD", "true")
	t.Setenv("GORUN_HSTS_INCLUDE_SUBDOMAINS", "false")
	t.Setenv("GORUN_UPDATE_SOURCE_URL", "")
	out.Reset()
	err = doctorCmd([]string{"--support", "--check-updates", "--data", filepath.Join(dir, "missing"), "--json"}, &out)
	if err == nil {
		t.Fatal("multiple invalid settings should fail")
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"secret_key", "trusted_proxies", "https_security", "data"} {
		requireDoctorStatus(t, report.Checks, id, "fail")
	}
	requireDoctorStatus(t, report.Checks, "updates", "warn")
}

func TestDoctorBackupAge(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		age    time.Duration
		files  []string
		status string
	}{
		{"missing", 0, nil, "warn"},
		{"partial", time.Hour, []string{".db"}, "warn"},
		{"recent", time.Hour, []string{".db", ".tar.gz"}, "ok"},
		{"one week", 7 * 24 * time.Hour, []string{".db", ".tar.gz"}, "ok"},
		{"stale", 7*24*time.Hour + time.Second, []string{".db", ".tar.gz"}, "warn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{DataDir: t.TempDir()}
			if len(tc.files) > 0 {
				dir := gpbackup.Dir(cfg.DataDir)
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				for _, suffix := range tc.files {
					path := filepath.Join(dir, now.Add(-tc.age).Format("20060102T150405Z")+suffix)
					if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			report := siteDoctorReport{OK: true}
			doctorBackup(cfg, now, &report)
			requireDoctorStatus(t, report.Checks, "backup_age", tc.status)
		})
	}
}

func TestDoctorDataDirChecks(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires unix permissions and flock")
	}
	dir := t.TempDir()
	lock, err := kernel.LockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	path := lock.Path()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	report := siteDoctorReport{OK: true}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	doctorDataDir(config.Config{DataDir: dir}, st, &report)
	requireDoctorStatus(t, report.Checks, "data_lock", "warn")
	requireDoctorStatus(t, report.Checks, "permissions", "fail")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("lock contents changed")
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	report = siteDoctorReport{OK: true}
	doctorDataDir(config.Config{DataDir: dir}, st, &report)
	requireDoctorStatus(t, report.Checks, "data_lock", "ok")
}

func TestDoctorDiskThreshold(t *testing.T) {
	for _, tc := range []struct {
		free   uint64
		ok     bool
		status string
	}{
		{0, false, "warn"},
		{1<<30 - 1, true, "warn"},
		{1 << 30, true, "ok"},
	} {
		report := siteDoctorReport{OK: true}
		doctorDiskSpace(tc.free, tc.ok, &report)
		requireDoctorStatus(t, report.Checks, "disk_space", tc.status)
	}
}

type doctorReleaseSource struct {
	update.Source
	latest string
	err    error
	calls  int
}

func (s *doctorReleaseSource) Latest(context.Context) (update.Artifact, error) {
	s.calls++
	return update.Artifact{Version: s.latest}, s.err
}

func TestDoctorUpdateCheck(t *testing.T) {
	for _, tc := range []struct {
		build, latest, status string
		err                   error
		calls                 int
	}{
		{"dev", "v1.2.0", "warn", nil, 0},
		{"v1.1.0", "v1.2.0", "warn", nil, 1},
		{"v1.2.0", "v1.2.0", "ok", nil, 1},
		{"v1.2.0", "v1.1.0", "ok", nil, 1},
		{"v1.2.0", "", "warn", errors.New("private URL or credentials"), 1},
	} {
		source := &doctorReleaseSource{latest: tc.latest, err: tc.err}
		report := siteDoctorReport{OK: true}
		doctorUpdates(context.Background(), source, tc.build, &report)
		check := requireDoctorStatus(t, report.Checks, "updates", tc.status)
		if source.calls != tc.calls || strings.Contains(check.Message, "private") {
			t.Fatalf("got %d calls and %+v", source.calls, check)
		}
	}
}
