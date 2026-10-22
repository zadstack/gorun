package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

const (
	remoteBackupLastSuccess = "storage.backup.last_success"
	scheduledBackupKind     = "backup.scheduled"
	remoteBackupTimeout     = 90 * time.Second
)

var errBackupScheduleWarning = errors.New("backup: local snapshot verified; review the saved schedule warning")

var errScheduledDestination = errors.New("backup: reselect the active backup destination before enabling scheduled uploads")

func validateScheduledDestination(ctx context.Context, tx *sql.Tx, d *kernel.Deps, id string) error {
	var activeRaw, destinationRaw string
	if err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key='storage.active.backup'`).Scan(&activeRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errScheduledDestination
		}
		return err
	}
	var active string
	if json.Unmarshal([]byte(activeRaw), &active) != nil || active != id {
		return errScheduledDestination
	}
	if err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, media.DestinationPrefix+id).Scan(&destinationRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errScheduledDestination
		}
		return err
	}
	var dest media.Destination
	if json.Unmarshal([]byte(destinationRaw), &dest) != nil || dest.ID != id || dest.Purpose != "backup" {
		return errScheduledDestination
	}
	if _, err := media.DestinationConfig(dest, d.Config.SecretKey); err != nil {
		return errScheduledDestination
	}
	return nil
}

// One in-flight job keeps slow uploads out of the serial cron scheduler.
// Upload failures remain visible until the next due snapshot; this job
// deliberately has one attempt so a retry cannot recreate the local pair.
func (h *handlers) backupScheduleTick(ctx context.Context) error {
	if !backupScheduleDue(ctx, h.d, time.Now().UTC()) {
		return nil
	}
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE kind=? AND status IN ('queued','running')`, scheduledBackupKind).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return nil
		}
		_, err := h.d.Jobs.EnqueueTx(ctx, tx, scheduledBackupKind, struct{}{})
		return err
	})
}

func (h *handlers) backupScheduledJob(ctx context.Context, _ json.RawMessage) error {
	return runScheduledBackup(ctx, h.d, media.SafeStorageClient())
}

func runScheduledBackup(ctx context.Context, d *kernel.Deps, client *http.Client) error {
	err := backupScheduleWithClient(ctx, d, time.Now().UTC(), client)
	if errors.Is(err, errBackupScheduleWarning) {
		return nil
	}
	if err != nil {
		return errors.New("backup: scheduled backup failed; inspect Backups and System Health")
	}
	return nil
}

func saveScheduledWarning(ctx context.Context, d *kernel.Deps, state backupScheduleState, cause error) error {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := d.Settings.Set(saveCtx, SettingBackupScheduleState, state); err != nil {
		return err
	}
	d.Audit.Record(saveCtx, "system.backup_schedule_warning", "backup", state.Stamp, map[string]string{"stage": state.Stage})
	d.Log.Warn("backup.schedule: verified with warning", "stamp", state.Stamp, "stage", state.Stage)
	return errors.Join(errBackupScheduleWarning, cause)
}

func scheduledRemoteUpload(ctx context.Context, d *kernel.Deps, res backup.Result, id string, client *http.Client) error {
	state := remoteBackupState{At: time.Now().UTC().Format(time.RFC3339), Stamp: res.Stamp, Status: "failed", DestinationID: id}
	dest, err := scheduledBackupDestination(ctx, d, id)
	if errors.Is(err, errScheduledDestination) {
		state.Status = "paused"
	}
	if err == nil {
		state.Destination = dest.Name
		var site string
		site, err = media.StorageSiteID(ctx, d.DB)
		if err == nil {
			uploadCtx, cancel := context.WithTimeout(ctx, remoteBackupTimeout)
			state.Receipt, err = uploadEncryptedBackup(uploadCtx, res, dest, d.Config.SecretKey, site, client)
			cancel()
			receiptErr := backup.StoreUploadReceipt(res, state.Receipt)
			if err == nil && receiptErr != nil {
				state.Status = "uploaded_unrecorded"
			}
			err = errors.Join(err, receiptErr)
		}
	}
	if err == nil {
		state.Status = "success"
	} else if state.Receipt.State == "partial" {
		state.Status = "partial"
	}
	// Cancellation must still leave the operator with the attempted outcome.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	saveErr := saveRemoteBackupState(saveCtx, d, state)
	d.Audit.Record(saveCtx, "system.backup_remote_scheduled", "backup", res.Stamp, map[string]string{"status": state.Status, "destination": id})
	return errors.Join(err, saveErr)
}

func scheduledBackupDestination(ctx context.Context, d *kernel.Deps, id string) (media.Destination, error) {
	if d.Settings.GetString(ctx, "storage.active.backup", "") != id {
		return media.Destination{}, errScheduledDestination
	}
	dest, err := media.LoadDestination(ctx, d.Settings, id)
	if err != nil || dest.Purpose != "backup" {
		return media.Destination{}, errScheduledDestination
	}
	return dest, nil
}

func saveRemoteBackupState(ctx context.Context, d *kernel.Deps, state remoteBackupState) error {
	return db.InTx(ctx, d.DB, func(tx *sql.Tx) error {
		if err := d.Settings.SetTx(ctx, tx, "storage.backup.last", state); err != nil {
			return err
		}
		if state.Status == "success" || state.Status == "uploaded_unrecorded" {
			return d.Settings.SetTx(ctx, tx, remoteBackupLastSuccess, state)
		}
		return nil
	})
}

func (h *handlers) registerScheduledBackups() {
	h.d.Jobs.Register(scheduledBackupKind, h.backupScheduledJob, 1)
	h.d.Jobs.Cron("backup.schedule", time.Hour, h.backupScheduleTick)
}
