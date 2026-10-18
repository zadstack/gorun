package admin

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
)

// backupMu serializes backups: two at once would race for the same timestamp.
var backupMu sync.Mutex

// backupsPage is the data of System → Backups.
type backupsPage struct {
	RemoteName string
	RemoteLast remoteBackupState
	Backups    []backupRow
	Dir        string
}

type backupRow struct {
	Stamp, Time, DBSize, FilesSize string
	HasDB, HasFiles                bool
	LocalState                     string
	HasManifest                    bool
	RestoredPath                   string
	Remote                         backup.UploadReceipt
}

// backupsList shows the backups in the data directory, newest first.
func (h *handlers) backupsList(w http.ResponseWriter, r *http.Request) {
	list, err := backup.List(h.d.Config.DataDir)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	d := backupsPage{Dir: backup.Dir(h.d.Config.DataDir)}
	if err := h.backupRemoteView(r.Context(), &d); err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, e := range list {
		row := backupRow{Stamp: e.Stamp, Time: e.Time.Format(time.RFC3339),
			DBSize: humanSize(e.DBSize), FilesSize: humanSize(e.FilesSize), HasDB: e.DBSize >= 0, HasFiles: e.FilesSize >= 0, LocalState: e.LocalState, HasManifest: e.HasManifest, Remote: e.Remote}
		if dest, ok := backup.Restored(h.d.Config.DataDir, e.Stamp); ok {
			row.RestoredPath = dest
		}
		d.Backups = append(d.Backups, row)
	}
	h.tpl.Render(w, r, "pages/system/backups.html", d)
}

// backupCreate makes a backup now (the same as `gorun backup`) and audits it.
func (h *handlers) backupCreate(w http.ResponseWriter, r *http.Request) {
	backupMu.Lock()
	res, err := backup.Create(h.d.Config.DataDir, h.d.Config.DBPath(), time.Now())
	backupMu.Unlock()
	if err != nil {
		h.d.Log.Error("admin: backup", "err", err)
		httpx.SetFlash(w, "error", "admin.backups.failed")
	} else {
		h.d.Audit.Record(r.Context(), "system.backup", "backup", res.Stamp, map[string]any{"files": res.Count})
		httpx.SetFlash(w, "success", "admin.backups.created")
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
