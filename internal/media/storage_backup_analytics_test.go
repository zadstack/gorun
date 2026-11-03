package media

import "testing"

func TestBackupObjectPatternAllowsAnalyticsCopy(t *testing.T) {
	prefix := "backups/01m4qaqm4rp3t0ysghn6n7hjws/2026/10/12/"
	for _, name := range []string{"20261012T120000Z.db.gorb", "20261012T120000Z.tar.gz.gorb", "20261012T120000Z-analytics.db.gorb"} {
		if !backupObjectPattern.MatchString(prefix + name) {
			t.Errorf("%s refused", name)
		}
	}
	for _, name := range []string{"20261012T120000Z-other.db.gorb", "analytics.db.gorb", "20261012T120000Z-analytics.db"} {
		if backupObjectPattern.MatchString(prefix + name) {
			t.Errorf("%s accepted", name)
		}
	}
}
