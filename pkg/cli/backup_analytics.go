package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
)

// analyticsBackupOptions reads the analytics.backup_exclude setting from the
// database at dbPath and returns the SkipAnalytics options for Create. CLI
// backups run without booting the site, so there is no settings service: the
// row is read directly over a read-only connection that never creates or
// migrates the file.
//
// It fails closed. No settings table or no row means the default (include).
// Any other failure (the database cannot be read, the value is not a JSON
// bool) leaves analytics.db out and sets SkipAnalyticsReason, so the backup
// reports the skip as a warning.
func analyticsBackupOptions(dbPath string) (skip bool, reason string) {
	setting, err := readAnalyticsExclude(dbPath)
	if err != nil {
		return true, gpbackup.AnalyticsSettingUnread + ": " + err.Error()
	}
	return setting, ""
}

func readAnalyticsExclude(dbPath string) (bool, error) {
	uri, err := db.FileURI(dbPath)
	if err != nil {
		return false, err
	}
	d, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false, err
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var tables int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'settings'`).Scan(&tables); err != nil {
		return false, err
	}
	if tables == 0 {
		return false, nil
	}
	var raw string
	err = d.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, gpbackup.SettingSkipAnalytics).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var skip bool
	if err := json.Unmarshal([]byte(raw), &skip); err != nil {
		return false, fmt.Errorf("%s is not a JSON bool", gpbackup.SettingSkipAnalytics)
	}
	return skip, nil
}
