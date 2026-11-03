package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

// analyticsBackupExcluded reads the analytics.backup_exclude setting from the
// database at dbPath. CLI backups run without booting the site, so there is
// no settings service: the row is read directly over a read-only connection
// that never creates or migrates the file. A missing database, table or row,
// or a value that is not a JSON bool, means "include" (the setting's default).
func analyticsBackupExcluded(dbPath string) bool {
	uri, err := db.FileURI(dbPath)
	if err != nil {
		return false
	}
	d, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var raw string
	if err := d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, gpbackup.SettingSkipAnalytics).Scan(&raw); err != nil {
		return false
	}
	var skip bool
	if json.Unmarshal([]byte(raw), &skip) != nil {
		return false
	}
	return skip
}
