package admin

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
)

// backupInspectPage shows a read-only preflight for one local backup pair.
type backupInspectPage struct {
	Stamp  string
	Dest   string
	Done   bool
	Report *backup.Inspection
}

// backupInspect runs Inspect on the stamp's manifest and renders the result.
func (h *handlers) backupInspect(w http.ResponseWriter, r *http.Request) {
	stamp := chi.URLParam(r, "stamp")
	manifest, err := backup.ManifestFile(h.d.Config.DataDir, stamp)
	if err != nil {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	rep, err := backup.Inspect(manifest)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := backupInspectPage{Stamp: stamp, Report: rep}
	if dest, ok := backup.Restored(h.d.Config.DataDir, stamp); ok {
		page.Dest, page.Done = dest, true
	} else if dest, err := backup.RestoreDestination(h.d.Config.DataDir, stamp); err == nil {
		page.Dest = dest
	}
	h.tpl.Render(w, r, "pages/system/backup_inspect.html", page)
}

// backupRestore writes the stamp's pair into <data>/restores/<stamp>/ only.
// It never merges into the live data directory (single-process SQLite lock).
// Recent password confirmation is required; after reauth the operator returns
// to the inspect page (POST cannot be replayed through /admin/reauth).
func (h *handlers) backupRestore(w http.ResponseWriter, r *http.Request) {
	stamp := chi.URLParam(r, "stamp")
	inspectPath := "/admin/system/backups/" + stamp + "/inspect"
	if !auth.RecentlyAuthenticated(r.Context(), auth.ReauthWindow) {
		http.Redirect(w, r, "/admin/reauth?next="+url.QueryEscape(inspectPath), http.StatusSeeOther)
		return
	}
	manifest, err := backup.ManifestFile(h.d.Config.DataDir, stamp)
	if err != nil {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	dest, err := backup.RestoreDestination(h.d.Config.DataDir, stamp)
	if err != nil {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		httpx.SetFlash(w, "error", "admin.backups.restore_exists")
		http.Redirect(w, r, inspectPath, http.StatusSeeOther)
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.serverError(w, r, err)
		return
	}
	// Restore opens the parent with os.OpenRoot; ensure restores/ exists.
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		h.serverError(w, r, err)
		return
	}
	rep, err := backup.Restore(manifest, dest)
	if err != nil {
		h.d.Log.Error("admin: backup restore", "err", err, "stamp", stamp)
		if rep != nil && rep.Readiness == "blocked" {
			httpx.SetFlash(w, "error", "admin.backups.restore_blocked")
		} else {
			httpx.SetFlash(w, "error", "admin.backups.restore_failed")
		}
		http.Redirect(w, r, inspectPath, http.StatusSeeOther)
		return
	}
	h.d.Audit.Record(r.Context(), "system.backup_restore", "backup", stamp, map[string]any{
		"dest":                    dest,
		"readiness":               rep.Readiness,
		"recovery_key_in_archive": rep.RecoveryKeyArchived,
	})
	httpx.SetFlash(w, "success", "admin.backups.restored")
	http.Redirect(w, r, inspectPath, http.StatusSeeOther)
}
