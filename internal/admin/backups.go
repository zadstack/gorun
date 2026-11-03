package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/update"
)

// backupMu serializes backups: two at once would race for the same timestamp.
var backupMu sync.Mutex

// backupsPage is the data of System → Backups.
type backupsPage struct {
	RemoteName           string
	RemoteID             string
	RemoteSuccess        remoteBackupState
	RemoteLast           remoteBackupState
	Schedule             backupSchedulePolicy
	Backups              []backupRow
	Dir                  string
	LastSuccess          backupSuccess
	UsedSpace, FreeSpace string
	Count                int
}

type backupRow struct {
	Stamp, Time, DBSize, FilesSize string
	AnalyticsSize                  string
	HasAnalytics                   bool
	HasDB, HasFiles                bool
	LocalState                     string
	HasManifest                    bool
	RestoredPath                   string
	Remote                         backup.UploadReceipt
}

// backupsList shows the backups in the data directory, newest first.
func (h *handlers) backupsList(w http.ResponseWriter, r *http.Request) {
	list, err := backup.ListWithKey(h.d.Config.DataDir, h.d.Config.SecretKey)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	d := backupsPage{Dir: backup.Dir(h.d.Config.DataDir)}
	if err := h.backupRemoteView(r.Context(), &d); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.backupScheduleView(r.Context(), &d)
	d.Count = len(list)
	var used int64
	if entries, err := os.ReadDir(d.Dir); err == nil {
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
				used += info.Size()
			}
		}
	}
	d.UsedSpace = humanSize(used)
	if free, _, ok := diskUsage(h.d.Config.DataDir); ok {
		d.FreeSpace = humanBytes(free)
	}
	for _, e := range list {
		row := backupRow{Stamp: e.Stamp, Time: e.Time.Format(time.RFC3339),
			DBSize: humanSize(e.DBSize), FilesSize: humanSize(e.FilesSize), HasDB: e.DBSize >= 0, HasFiles: e.FilesSize >= 0,
			AnalyticsSize: humanSize(e.AnalyticsSize), HasAnalytics: e.AnalyticsSize >= 0, LocalState: e.LocalState, HasManifest: e.HasManifest, Remote: e.Remote}
		if dest, ok := backup.Restored(h.d.Config.DataDir, e.Stamp); ok {
			row.RestoredPath = dest
		}
		d.Backups = append(d.Backups, row)
	}
	h.tpl.Render(w, r, "pages/system/backups.html", d)
}

// backupCreate makes a backup now (the same as `gorun backup`) and audits it.
func (h *handlers) backupCreate(w http.ResponseWriter, r *http.Request) {
	release, err := h.holdDataLease(r.Context(), update.HolderBackup)
	if err != nil {
		if leaseHeld(err) {
			httpx.SetFlash(w, "error", "admin.backups.busy")
			http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
			return
		}
		h.serverError(w, r, err)
		return
	}
	defer release()
	backupMu.Lock()
	now := time.Now()
	res, err := backup.CreateWith(h.d.Config.DataDir, h.d.Config.DBPath(), now, backupOptions(r.Context(), h.d))
	if err == nil {
		var success backupSuccess
		success, err = verifiedBackup(res, now, h.d.Config.SecretKey)
		if err == nil {
			err = h.d.Settings.Set(r.Context(), SettingBackupLastSuccess, success)
		}
	}
	backupMu.Unlock()
	if err != nil {
		h.d.Log.Error("admin: backup", "err", err)
		h.d.Audit.Record(r.Context(), "system.backup_failed", "backup", "manual", nil)
		httpx.SetFlash(w, "error", "admin.backups.failed")
	} else {
		h.d.Audit.Record(r.Context(), "system.backup", "backup", res.Stamp, map[string]any{"files": res.Count, "verified": true})
		if noteAnalyticsSkipped(r.Context(), h.d, res, "manual") {
			// httpx has no warning kind; info carries the warning text.
			httpx.SetFlash(w, "info", "admin.backups.analytics_skipped")
		} else {
			httpx.SetFlash(w, "success", "admin.backups.created")
		}
	}
	http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
}

// backupDownload serves one backup file by its exact name; any other name is a 404.
func (h *handlers) backupDownload(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, err := backup.File(h.d.Config.DataDir, name)
	if err != nil {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.d.Audit.Record(r.Context(), "system.backup_download", "backup", name, nil)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(p)))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, p)
}

// humanSize formats a byte count ("" for a missing file).
func humanSize(n int64) string {
	switch {
	case n < 0:
		return ""
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

// backupOptions are the Create options every admin backup uses: the site
// secret for the manifest HMAC, and SkipAnalytics from the setting
// analytics.backup_exclude (read by name; admin never imports analytics).
// The setting fails closed: if it cannot be read (or is not a JSON bool),
// analytics.db is left out and the skip is reported as a warning, so an
// operator who excluded analytics never gets it copied by accident.
func backupOptions(ctx context.Context, d *kernel.Deps) backup.Options {
	opts := backup.Options{SecretKey: d.Config.SecretKey}
	var skip bool
	err := d.Settings.Get(ctx, backup.SettingSkipAnalytics, &skip)
	switch {
	case err == nil:
		opts.SkipAnalytics = skip
	case errors.Is(err, settings.ErrNotFound):
	default:
		d.Log.Warn("admin: backup: cannot read analytics exclusion; leaving analytics.db out", "err", err)
		opts.SkipAnalytics = true
		opts.SkipAnalyticsReason = backup.AnalyticsSettingUnread + ": " + err.Error()
	}
	return opts
}

// noteAnalyticsSkipped audits and logs a backup that left analytics.db out
// involuntarily (see backup.Result.AnalyticsSkipped). It reports whether
// there was such a skip. Only the reason's code reaches the audit log.
func noteAnalyticsSkipped(ctx context.Context, d *kernel.Deps, res backup.Result, source string) bool {
	if res.AnalyticsSkipped == "" {
		return false
	}
	code, _, _ := strings.Cut(res.AnalyticsSkipped, ":")
	d.Audit.Record(ctx, "system.backup_analytics_skipped", "backup", res.Stamp, map[string]string{"source": source, "code": code})
	d.Log.Warn("backup: analytics.db left out of the backup", "stamp", res.Stamp, "source", source, "reason", res.AnalyticsSkipped)
	return true
}
