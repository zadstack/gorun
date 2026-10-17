package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

type storagePage struct {
	Profiles              []connectionView
	MediaName, BackupName string
	MediaID, BackupID     string
}

func (h *handlers) storageSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.listConnections(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := storagePage{}
	for _, row := range rows {
		if row.Kind == "s3" {
			page.Profiles = append(page.Profiles, viewConnection(row))
		}
	}
	for _, dest := range []struct {
		purpose string
		name    *string
		id      *string
	}{{"media", &page.MediaName, &page.MediaID}, {"backup", &page.BackupName, &page.BackupID}} {
		var id string
		err = h.d.Settings.Get(r.Context(), "storage.active."+dest.purpose, &id)
		if errors.Is(err, settings.ErrNotFound) || err == nil && id == "" {
			continue
		}
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		d, err := media.LoadDestination(r.Context(), h.d.Settings, id)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		*dest.name = d.Name
		*dest.id = id
	}
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.Render(w, r, "pages/settings/storage.html", page)
}
func (h *handlers) storageActivate(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		h.tpl.RenderError(w, r, 400)
		return
	}
	c, err := h.loadConnection(r.Context(), r.PostForm.Get("id"))
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	if c.Version != r.PostForm.Get("version") {
		h.connectionError(w, r, errConnectionConflict)
		return
	}
	if c.Kind != "s3" || validateConnection(c) != nil {
		h.tpl.RenderError(w, r, 422)
		return
	}
	secrets, err := h.connectionSecrets(c)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	dest, err := media.PrepareDestination(c.Name, r.PostForm.Get("purpose"), media.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: secrets.AccessKey, SecretKey: secrets.SecretKey}, h.d.Config.SecretKey)
	if err != nil {
		h.tpl.RenderError(w, r, 422)
		return
	}
	err = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		var version string
		if err := tx.QueryRowContext(r.Context(), `SELECT json_extract(value_json,'$.version') FROM settings WHERE key=?`, connectionPrefix+c.ID).Scan(&version); err != nil {
			return err
		}
		if version != c.Version {
			return errConnectionConflict
		}
		if err := media.SaveDestinationTx(r.Context(), tx, h.d.Settings, dest); err != nil {
			return err
		}
		h.d.Audit.RecordTx(r.Context(), tx, "storage.activate", "destination", dest.ID, map[string]string{"purpose": dest.Purpose, "connection": c.ID})
		return nil
	})
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.storage.activated")
	redirect(w, r, "/admin/settings/storage")
}
func (h *handlers) storageDisable(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		h.tpl.RenderError(w, r, 400)
		return
	}
	purpose := r.PostForm.Get("purpose")
	if purpose != "media" && purpose != "backup" {
		h.tpl.RenderError(w, r, 400)
		return
	}
	expected := r.PostForm.Get("destination_id")
	err := db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(r.Context(), "SELECT value_json FROM settings WHERE key=?", "storage.active."+purpose).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errConnectionConflict
			}
			return err
		}
		var current string
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return err
		}
		if expected == "" || current != expected {
			return errConnectionConflict
		}
		if err := h.d.Settings.SetTx(r.Context(), tx, "storage.active."+purpose, ""); err != nil {
			return err
		}
		h.d.Audit.RecordTx(r.Context(), tx, "storage.disable", "destination", purpose, nil)
		return nil
	})
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.storage.disabled")
	redirect(w, r, "/admin/settings/storage")
}
