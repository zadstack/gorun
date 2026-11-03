package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/analytics"
	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

// analyticsDataDir is a data directory with gorun.db (holding a settings
// table, optionally with analytics.backup_exclude) and an analytics.db.
func analyticsDataDir(t *testing.T, exclude string) string {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "gorun.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if exclude != "" {
		if _, err := d.Exec(`INSERT INTO settings VALUES (?, ?, '2026-11-03T00:00:00Z')`, gpbackup.SettingSkipAnalytics, exclude); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	a, err := db.Open(filepath.Join(dir, gpbackup.AnalyticsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	return dir
}

func backupHasAnalytics(t *testing.T, dir string) bool {
	t.Helper()
	list, err := gpbackup.List(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	return list[0].AnalyticsSize >= 0
}

func TestBackupCLIAnalyticsMember(t *testing.T) {
	for _, tc := range []struct {
		name, setting string
		args          []string
		want          bool
	}{
		{"default includes", "", nil, true},
		{"setting false includes", "false", nil, true},
		{"setting true skips", "true", nil, false},
		{"flag skips", "", []string{"--skip-analytics"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := analyticsDataDir(t, tc.setting)
			var out bytes.Buffer
			if err := backup(append([]string{"--data", dir}, tc.args...), &out); err != nil {
				t.Fatal(err)
			}
			if got := backupHasAnalytics(t, dir); got != tc.want {
				t.Fatalf("analytics member = %v, want %v\n%s", got, tc.want, out.String())
			}
			if !strings.Contains(out.String(), "analytics:") {
				t.Fatalf("output does not report analytics:\n%s", out.String())
			}
		})
	}
}

// `gorun staging --from` honours the source site's analytics.backup_exclude
// and otherwise restores analytics.db into the new data directory.
func TestStagingAnalyticsFollowsSourceSetting(t *testing.T) {
	for _, tc := range []struct {
		setting string
		want    bool
	}{{"", true}, {"true", false}} {
		from := analyticsDataDir(t, tc.setting)
		dest := filepath.Join(t.TempDir(), "staged")
		var out bytes.Buffer
		if err := stagingCmd([]string{"seed", "--from", from, "--dest", dest}, &out); err != nil {
			t.Fatal(err, out.String())
		}
		_, err := os.Stat(filepath.Join(dest, gpbackup.AnalyticsFile))
		if got := err == nil; got != tc.want {
			t.Fatalf("setting %q: analytics.db restored = %v, want %v", tc.setting, got, tc.want)
		}
	}
}

// The backup package knows the setting only by name; it must stay the name
// the analytics settings page writes.
func TestAnalyticsExcludeSettingNameMatches(t *testing.T) {
	if gpbackup.SettingSkipAnalytics != analytics.SettingBackupExclude {
		t.Fatalf("backup reads %q, analytics writes %q", gpbackup.SettingSkipAnalytics, analytics.SettingBackupExclude)
	}
	if gpbackup.AnalyticsFile != analytics.DBFile {
		t.Fatalf("backup copies %q, analytics owns %q", gpbackup.AnalyticsFile, analytics.DBFile)
	}
}

// An unreadable exclusion setting fails closed: analytics is left out and the
// skip is reported, while the core backup succeeds.
func TestBackupCLIUnreadableSettingSkipsAnalytics(t *testing.T) {
	dir := analyticsDataDir(t, `"yes"`)
	var out bytes.Buffer
	if err := backup([]string{"--data", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if backupHasAnalytics(t, dir) || !strings.Contains(out.String(), "analytics: skipped ("+gpbackup.AnalyticsSettingUnread) {
		t.Fatalf("unreadable setting did not fail closed:\n%s", out.String())
	}
}

func TestBackupCLICorruptAnalyticsStillBacksUp(t *testing.T) {
	dir := analyticsDataDir(t, "")
	if err := os.WriteFile(filepath.Join(dir, gpbackup.AnalyticsFile), []byte(strings.Repeat("garbage ", 600)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-wal", "-shm"} {
		os.Remove(filepath.Join(dir, gpbackup.AnalyticsFile+s))
	}
	var out bytes.Buffer
	if err := backup([]string{"--data", dir}, &out); err != nil {
		t.Fatalf("core backup failed because of analytics: %v", err)
	}
	if backupHasAnalytics(t, dir) || !strings.Contains(out.String(), "analytics: skipped ("+gpbackup.AnalyticsCopyFailed) {
		t.Fatalf("corrupt analytics not reported:\n%s", out.String())
	}
}
