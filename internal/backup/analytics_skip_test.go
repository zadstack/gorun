package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A broken analytics.db never fails the core backup: the copy is left out,
// the manifest has no member, and Result.AnalyticsSkipped says why.
func TestBackupSkipsBrokenAnalytics(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		setup      func(t *testing.T, dir string)
	}{
		{"corrupt", AnalyticsCopyFailed, func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, AnalyticsFile), []byte(strings.Repeat("not a database ", 400)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", AnalyticsNotRegular, func(t *testing.T, dir string) {
			target := filepath.Join(t.TempDir(), "elsewhere.db")
			analyticsSource(t, filepath.Dir(target), 1)
			if err := os.Rename(filepath.Join(filepath.Dir(target), AnalyticsFile), target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, AnalyticsFile)); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", AnalyticsNotRegular, func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, AnalyticsFile), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := backupSource(t)
			tc.setup(t, dir)
			res, err := Create(dir, path, time.Now())
			if err != nil {
				t.Fatalf("core backup failed because of analytics: %v", err)
			}
			assertSkipped(t, dir, res, tc.code)
		})
	}
}

func TestBackupSkipsAnalyticsCopyFailingIntegrity(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 3)
	old := checkAnalyticsCopy
	checkAnalyticsCopy = func(string) error { return errors.New("simulated damage") }
	t.Cleanup(func() { checkAnalyticsCopy = old })
	res, err := Create(dir, path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertSkipped(t, dir, res, AnalyticsIntegrityFailed)
}

func TestBackupSkipReasonFromCaller(t *testing.T) {
	dir, path := backupSource(t)
	analyticsSource(t, dir, 1)
	res, err := CreateWith(dir, path, time.Now(), Options{SkipAnalytics: true, SkipAnalyticsReason: AnalyticsSettingUnread + ": boom"})
	if err != nil {
		t.Fatal(err)
	}
	assertSkipped(t, dir, res, AnalyticsSettingUnread)
	// A deliberate exclusion is not a warning.
	res, err = CreateWith(dir, path, time.Now().Add(time.Second), Options{SkipAnalytics: true})
	if err != nil || res.AnalyticsSkipped != "" || res.Analytics != "" {
		t.Fatalf("deliberate exclusion: %+v %v", res, err)
	}
}

func assertSkipped(t *testing.T, dir string, res Result, code string) {
	t.Helper()
	if !strings.HasPrefix(res.AnalyticsSkipped, code+": ") {
		t.Fatalf("AnalyticsSkipped = %q, want code %s", res.AnalyticsSkipped, code)
	}
	if res.Analytics != "" {
		t.Fatalf("Analytics = %q", res.Analytics)
	}
	if _, err := os.Lstat(filepath.Join(Dir(dir), res.Stamp+analyticsSuffix)); !os.IsNotExist(err) {
		t.Fatal("partial analytics copy left behind")
	}
	m, err := ReadManifest(res.Manifest)
	if err != nil || m.Analytics != nil {
		t.Fatalf("manifest: %+v %v", m, err)
	}
	rep, err := Inspect(res.Manifest)
	if err != nil || rep.Readiness == "blocked" || !rep.IntegrityOK || rep.Analytics {
		t.Fatalf("inspect: %+v %v", rep, err)
	}
}
