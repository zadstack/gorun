package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
