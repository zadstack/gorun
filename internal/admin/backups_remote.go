package admin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

type remoteBackupState struct {
	Stamp       string               `json:"stamp"`
	At          string               `json:"at"`
	Status      string               `json:"status"`
	Destination string               `json:"destination"`
	Receipt     backup.UploadReceipt `json:"receipt"`
}

func (h *handlers) backupRemoteView(ctx context.Context, page *backupsPage) error {
	var id string
	err := h.d.Settings.Get(ctx, "storage.active.backup", &id)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return err
	}
	if id != "" {
		d, err := media.LoadDestination(ctx, h.d.Settings, id)
		if err != nil {
			return err
		}
		page.RemoteName = d.Name
	}
	err = h.d.Settings.Get(ctx, "storage.backup.last", &page.RemoteLast)
	if errors.Is(err, settings.ErrNotFound) {
		return nil
	}
	return err
}
func uploadEncryptedBackup(ctx context.Context, res backup.Result, d media.Destination, secret []byte, site string, client *http.Client) (receipt backup.UploadReceipt, resultErr error) {
	at := time.Now().UTC().Format(time.RFC3339)
	receipt = backup.UploadReceipt{State: "failed", Files: []backup.UploadFile{{Name: filepath.Base(res.DB), State: "not_attempted", CheckedAt: at}, {Name: filepath.Base(res.Files), State: "not_attempted", CheckedAt: at}}}
	defer func() {
		uploaded := 0
		for _, f := range receipt.Files {
			if f.State == "uploaded" {
				uploaded++
			}
		}
		if uploaded == 2 {
			receipt.State = "uploaded"
		} else if uploaded == 1 {
			receipt.State = "partial"
		}
	}()

	if d.Purpose != "backup" {
		return receipt, errors.New("backup: wrong storage purpose")
	}
	cfg, err := media.DestinationConfig(d, secret)
	if err != nil {
		return receipt, err
	}
	driver, err := media.NewS3Storage(cfg, nil, false)
	if err != nil {
		return receipt, err
	}
	stamp, err := time.Parse("20060102T150405Z", res.Stamp)
	if err != nil {
		return receipt, err
	}
	for i, path := range []string{res.DB, res.Files} {
		st, err := os.Lstat(path)
		if err != nil {
			return receipt, err
		}
		if !st.Mode().IsRegular() {
			return receipt, errors.New("backup: upload requires a regular backup file")
		}
		if st.Size() > backup.MaxEncryptedFile {
			return receipt, errors.New("backup: encrypted upload limit exceeded; local backup retained")
		}
		f, err := os.Open(path)
		if err != nil {
			return receipt, err
		}
		body, encryptErr := backup.Encrypt(f, secret, filepath.Base(path))
		closeErr := f.Close()
		if encryptErr != nil {
			return receipt, encryptErr
		}
		if closeErr != nil {
			return receipt, closeErr
		}
		key := "backups/" + site + "/" + stamp.Format("2006/01/02") + "/" + filepath.Base(path) + ".gorb"
		if err = driver.PutEncryptedBackup(ctx, client, key, body); err != nil {
			receipt.Files[i].State, receipt.Files[i].Code = "failed", "provider_upload_failed"
			if i == 1 {
				receipt.State = "partial"
			}
			return receipt, err
		}
		receipt.Files[i].State = "uploaded"
	}
	receipt.State = "uploaded"
	return receipt, nil
}
func (h *handlers) backupRemoteCreate(w http.ResponseWriter, r *http.Request) {
	backupMu.Lock()
	defer backupMu.Unlock()
	id := h.d.Settings.GetString(r.Context(), "storage.active.backup", "")
	if id == "" {
		httpx.SetFlash(w, "error", "admin.storage.remote_unconfigured")
		redirect(w, r, "/admin/system/backups")
		return
	}
	dest, err := media.LoadDestination(r.Context(), h.d.Settings, id)
	if err != nil || dest.Purpose != "backup" {
		h.tpl.RenderError(w, r, 422)
		return
	}
	if _, err = media.DestinationConfig(dest, h.d.Config.SecretKey); err != nil {
		h.serverError(w, r, err)
		return
	}
	site, err := media.StorageSiteID(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	state := remoteBackupState{At: time.Now().UTC().Format(time.RFC3339), Status: "failed", Destination: dest.Name}
	res, err := backup.Create(h.d.Config.DataDir, h.d.Config.DBPath(), time.Now())
	if err == nil {
		state.Stamp = res.Stamp
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		state.Receipt, err = uploadEncryptedBackup(ctx, res, dest, h.d.Config.SecretKey, site, media.SafeStorageClient())
		if state.Receipt.State == "partial" {
			state.Status = "partial"
		}
		if receiptErr := backup.StoreUploadReceipt(res, state.Receipt); receiptErr != nil {
			err = errors.Join(err, receiptErr)
		}
		cancel()
	}
	if err == nil {
		state.Status = "success"
	}
	if saveErr := h.d.Settings.Set(r.Context(), "storage.backup.last", state); saveErr != nil {
		h.serverError(w, r, saveErr)
		return
	}
	h.d.Audit.Record(r.Context(), "system.backup_remote", "backup", state.Stamp, map[string]string{"status": state.Status, "destination": id})
	if err != nil {
		h.d.Log.Error("admin: encrypted backup upload failed", "error", err)
		httpx.SetFlash(w, "error", "admin.storage.remote_failed")
	} else {
		httpx.SetFlash(w, "success", "admin.storage.remote_success")
	}
	redirect(w, r, "/admin/system/backups")
}
