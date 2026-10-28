package admin

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
)

type remoteBackupState struct {
	Stamp         string               `json:"stamp"`
	At            string               `json:"at"`
	Status        string               `json:"status"`
	DestinationID string               `json:"destination_id"`
	Destination   string               `json:"destination"`
	Receipt       backup.UploadReceipt `json:"receipt"`
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
		page.RemoteID = d.ID
	}
	if err := h.d.Settings.Get(ctx, remoteBackupLastSuccess, &page.RemoteSuccess); err != nil && !errors.Is(err, settings.ErrNotFound) {
		return err
	}
	err = h.d.Settings.Get(ctx, "storage.backup.last", &page.RemoteLast)
	if errors.Is(err, settings.ErrNotFound) {
		return nil
	}
	return err
}
func uploadEncryptedBackup(ctx context.Context, res backup.Result, d media.Destination, secret []byte, site string, client *http.Client) (receipt backup.UploadReceipt, resultErr error) {
	at := time.Now().UTC().Format(time.RFC3339)
	receipt = backup.UploadReceipt{State: "failed", DestinationID: d.ID, Files: []backup.UploadFile{{Name: filepath.Base(res.DB), State: "not_attempted", CheckedAt: at}, {Name: filepath.Base(res.Files), State: "not_attempted", CheckedAt: at}}}
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
	manifest, err := backup.ReadManifest(res.Manifest)
	if err != nil {
		return receipt, err
	}
	valid, err := backup.CheckManifestHMAC(manifest, secret)
	if err != nil || !valid || manifest.Stamp != res.Stamp {
		return receipt, errors.New("backup: authenticated local manifest is required")
	}
	expected := []backup.FileDigest{manifest.Database, manifest.Archive}
	for i, path := range []string{res.DB, res.Files} {
		if filepath.Base(path) != expected[i].Name || filepath.Dir(path) != filepath.Dir(res.Manifest) {
			return receipt, errors.New("backup: manifest file binding mismatch")
		}
		object := "backups/" + site + "/" + stamp.Format("2006/01/02") + "/" + filepath.Base(path) + ".gorb"
		receipt.Files[i].ObjectKey = object
		request := encryptedBackupTransfer{driver: driver, client: client, path: path, object: object, secret: secret, expected: expected[i]}
		acknowledged, err := request.transfer(ctx)
		if acknowledged {
			receipt.Files[i].State = "uploaded"
		} else {
			receipt.Files[i].State = "failed"
		}
		if err != nil {
			receipt.Files[i].Code = "provider_upload_failed"
			if acknowledged {
				receipt.Files[i].Code = "provider_readback_failed"
			}
			return receipt, err
		}
	}
	receipt.ReadBackVerified = true
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
	state := remoteBackupState{At: time.Now().UTC().Format(time.RFC3339), Status: "failed", Destination: dest.Name, DestinationID: dest.ID}
	res, err := backup.CreateWith(h.d.Config.DataDir, h.d.Config.DBPath(), time.Now(), backup.Options{SecretKey: h.d.Config.SecretKey})
	if err == nil {
		state.Stamp = res.Stamp
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		state.Receipt, err = uploadEncryptedBackup(ctx, res, dest, h.d.Config.SecretKey, site, media.SafeStorageClient())
		if state.Receipt.State == "partial" {
			state.Status = "partial"
		}
		if receiptErr := backup.StoreUploadReceiptWithKey(res, state.Receipt, h.d.Config.SecretKey); receiptErr != nil {
			if err == nil && state.Receipt.State == "uploaded" {
				state.Status = "uploaded_unrecorded"
			}
			err = errors.Join(err, receiptErr)
		}
		cancel()
	}
	if err == nil {
		state.Status = "success"
	}
	if saveErr := saveRemoteBackupState(r.Context(), h.d, state); saveErr != nil {
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

func (state remoteBackupState) StatusKey() string {
	switch state.Status {
	case "success":
		return "admin.storage.remote_success"
	case "uploaded_unrecorded":
		return "remotebackup.uploaded_unrecorded"
	case "paused":
		return "remotebackup.paused"
	default:
		return "admin.storage.remote_failed"
	}
}
