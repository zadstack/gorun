package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/update"
)

// BackupRun is the outcome of RunBackupNow. Destination is "local" or the
// id of the destination the pair was uploaded to; Remote is that upload's
// status (success, failed, partial, paused or uploaded_unrecorded, empty
// when local) and Objects its object keys. Warning names a stage that did
// not stop the backup (analytics, remote or prune).
type BackupRun struct {
	Stamp       string
	At          time.Time
	Bytes       int64
	Duration    time.Duration
	Destination string
	Remote      string
	Objects     []string
	Pruned      int
	Warning     string
}

// ErrBackupBusy is a backup refused because another operation (an update,
// a restore, another backup) holds the data directory.
var ErrBackupBusy = errors.New("backup: the data directory is busy")

// ErrBackupRemote is a backup kept locally whose upload did not succeed.
var ErrBackupRemote = errors.New("backup: the upload to the remote destination did not succeed")

// RunBackupNow takes a verified backup now, as a scheduled run does but
// whether or not one is due: it records backup.local.last_success, uploads
// the pair to the scheduled remote destination
// (backup.schedule.remote_destination) when one is set, and keeps the
// newest backup.retain pairs. The schedule's own clock is left as it is.
// client is the HTTP client of the upload (nil: the guarded storage
// client). via is the surface that asked, added to the audit records.
// A failed upload returns the run with ErrBackupRemote: the local pair
// stays.
func RunBackupNow(ctx context.Context, d *kernel.Deps, client *http.Client, via string) (BackupRun, error) {
	start := time.Now()
	now := start.UTC()
	if client == nil {
		client = media.SafeStorageClient()
	}
	if svc, ok := kernel.Get[*update.Service](d, update.ServiceName); ok {
		owner := update.HolderBackup + ":service:" + update.NewOwnerToken()
		if err := svc.Lease().Acquire(ctx, update.HolderBackup, owner, ""); err != nil {
			var held *update.ErrLeaseHeld
			if errors.As(err, &held) {
				return BackupRun{}, ErrBackupBusy
			}
			return BackupRun{}, err
		}
		defer svc.Lease().Release(context.WithoutCancel(ctx), owner)
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	res, err := backup.CreateWith(d.Config.DataDir, d.Config.DBPath(), now, backupOptions(ctx, d))
	if err != nil {
		d.Audit.Record(ctx, "system.backup_failed", "backup", "service", map[string]string{"stage": "create", "via": via})
		return BackupRun{}, err
	}
	success, err := verifiedBackup(res, now, d.Config.SecretKey)
	if err != nil {
		d.Audit.Record(ctx, "system.backup_failed", "backup", "service", map[string]string{"stage": "verify", "via": via})
		return BackupRun{}, err
	}
	if err := d.Settings.Set(ctx, SettingBackupLastSuccess, success); err != nil {
		return BackupRun{}, err
	}
	d.Audit.Record(ctx, "system.backup", "backup", res.Stamp, map[string]any{"files": res.Count, "verified": true, "via": via})
	run := BackupRun{Stamp: res.Stamp, At: now, Bytes: success.Bytes, Destination: "local"}
	if noteAnalyticsSkipped(ctx, d, res, "service") {
		run.Warning = "analytics"
	}
	var remoteErr error
	if id := d.Settings.GetString(ctx, SettingBackupRemoteSchedule, ""); id != "" {
		state, err := remoteUpload(ctx, d, res, id, client, "system.backup_remote", map[string]string{"via": via})
		run.Destination, run.Remote = id, state.Status
		for _, f := range state.Receipt.Files {
			if f.ObjectKey != "" {
				run.Objects = append(run.Objects, f.ObjectKey)
			}
		}
		if err != nil {
			d.Log.Error("backup: service run upload failed", "stamp", res.Stamp, "status", state.Status, "err", err)
			run.Warning, remoteErr = "remote", ErrBackupRemote
		}
	}
	if keep := backupRetain(ctx, d); keep > 0 {
		pruned, err := backup.PruneVerifiedKeepingWith(d.Config.DataDir, keep, res.Stamp, d.Config.SecretKey)
		if err != nil {
			d.Audit.Record(ctx, "system.backup_retention_failed", "backup", res.Stamp, map[string]string{"stage": "prune", "via": via})
			d.Log.Error("backup: service run retention failed", "err", err)
			if run.Warning == "" {
				run.Warning = "prune"
			}
		} else if pruned > 0 {
			run.Pruned = pruned
			d.Audit.Record(ctx, "system.backup_retention", "backup", res.Stamp, map[string]any{"pruned": pruned, "retain": keep, "via": via})
		}
	}
	run.Duration = time.Since(start)
	return run, remoteErr
}
