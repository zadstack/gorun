package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/update"
)

// Local backup schedule / retention settings (System → Backups).
const (
	SettingBackupScheduleHours  = "backup.schedule_hours"
	SettingBackupRetain         = "backup.retain"
	SettingBackupScheduleLast   = "backup.schedule.last"
	SettingBackupScheduleState  = "backup.schedule.state"
	SettingBackupLastSuccess    = "backup.local.last_success"
	SettingBackupRemoteSchedule = "backup.schedule.remote_destination"
)

const (
	maxScheduleHours = 168 // one week
	maxRetainPairs   = 100
)

// backupSchedulePolicy is the schedule form state on the backups page.
type backupSchedulePolicy struct {
	Hours             int
	Retain            int
	Last              string
	NextHint          string
	Enabled           bool
	State             backupScheduleState
	RemoteDestination string
}

func backupScheduleHours(ctx context.Context, d *kernel.Deps) int {
	var n int
	if err := d.Settings.Get(ctx, SettingBackupScheduleHours, &n); err != nil || n < 1 {
		return 0
	}
	if n > maxScheduleHours {
		return maxScheduleHours
	}
	return n
}

func backupRetain(ctx context.Context, d *kernel.Deps) int {
	var n int
	if err := d.Settings.Get(ctx, SettingBackupRetain, &n); err != nil || n < 1 {
		return 0
	}
	if n > maxRetainPairs {
		return maxRetainPairs
	}
	return n
}

func backupScheduleLast(ctx context.Context, d *kernel.Deps) time.Time {
	var s string
	if err := d.Settings.Get(ctx, SettingBackupScheduleLast, &s); err != nil || s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func (h *handlers) backupScheduleView(ctx context.Context, page *backupsPage) {
	hours := backupScheduleHours(ctx, h.d)
	retain := backupRetain(ctx, h.d)
	last := backupScheduleLast(ctx, h.d)
	p := backupSchedulePolicy{Hours: hours, Retain: retain, Enabled: hours >= 1}
	if !last.IsZero() {
		p.Last = last.Format(time.RFC3339)
		if hours >= 1 {
			p.NextHint = last.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339)
		}
	}
	_ = h.d.Settings.Get(ctx, SettingBackupScheduleState, &p.State)
	p.RemoteDestination = h.d.Settings.GetString(ctx, SettingBackupRemoteSchedule, "")
	page.Schedule = p
	_ = h.d.Settings.Get(ctx, SettingBackupLastSuccess, &page.LastSuccess)
}

// backupScheduleSave stores schedule hours and local retain-N from the form.
func (h *handlers) backupScheduleSave(w http.ResponseWriter, r *http.Request) {
	hours, errH := strconv.Atoi(r.FormValue("schedule_hours"))
	retain, errR := strconv.Atoi(r.FormValue("retain"))
	if errH != nil || hours < 0 || hours > maxScheduleHours {
		httpx.SetFlash(w, "error", "admin.backups.schedule_invalid")
		http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
		return
	}
	if errR != nil || retain < 0 || retain > maxRetainPairs {
		httpx.SetFlash(w, "error", "admin.backups.schedule_invalid")
		http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	remoteID := r.FormValue("remote_destination")
	err := saveBackupSchedule(ctx, h.d, backupPolicy{Hours: hours, Retain: retain, RemoteDestination: remoteID})
	if err != nil {
		if errors.Is(err, errScheduledDestination) {
			httpx.SetFlash(w, "error", "remotebackup.invalid_destination")
			http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.backups.schedule_saved")
	http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
}

// backupScheduleAt runs one scheduled local backup when due (injected clock for tests).
func backupScheduleAt(ctx context.Context, d *kernel.Deps, now time.Time) error {
	return backupScheduleWithClient(ctx, d, now, media.SafeStorageClient())
}

func backupScheduleWithClient(ctx context.Context, d *kernel.Deps, now time.Time, client *http.Client) error {
	now = now.UTC()
	if !backupScheduleDue(ctx, d, now) {
		return nil
	}
	renewLease := func() error { return nil }
	if svc, ok := kernel.Get[*update.Service](d, update.ServiceName); ok {
		owner := update.HolderBackup + ":schedule:" + update.NewOwnerToken()
		if err := svc.Lease().Acquire(ctx, update.HolderBackup, owner, ""); err != nil {
			var held *update.ErrLeaseHeld
			if errors.As(err, &held) {
				d.Log.Info("backup: scheduled backup skipped, data directory busy", "holder", held.Holder, "operation", held.OperationID)
				return nil
			}
			return err
		}
		defer svc.Lease().Release(context.WithoutCancel(ctx), owner)
		renewLease = func() error { return svc.Lease().Renew(ctx, owner) }
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	// Recheck after acquiring the lock so a queued job respects an operator opt-out.
	if !backupScheduleDue(ctx, d, now) {
		return nil
	}
	state := backupScheduleState{AttemptAt: now.Format(time.RFC3339), Status: "running", Stage: "create"}
	if err := d.Settings.Set(ctx, SettingBackupScheduleState, state); err != nil {
		return err
	}
	fail := func(stage string, cause error) error {
		state.Status, state.Stage = "failed", stage
		saveErr := d.Settings.Set(ctx, SettingBackupScheduleState, state)
		d.Audit.Record(ctx, "system.backup_schedule_failed", "backup", "schedule", map[string]string{"stage": stage})
		d.Log.Error("backup.schedule: failed", "stage", stage, "err", cause)
		return errors.Join(cause, saveErr)
	}
	res, err := backup.Create(d.Config.DataDir, d.Config.DBPath(), now)
	if err != nil {
		return fail("create", err)
	}
	state.Stamp = res.Stamp
	success, err := verifiedBackup(res, now)
	if err != nil {
		return fail("verify", err)
	}
	if err := renewLease(); err != nil {
		return fail("record", err)
	}
	state.Status, state.Stage = "success", "verified"
	err = db.InTx(ctx, d.DB, func(tx *sql.Tx) error {
		if err := d.Settings.SetTx(ctx, tx, SettingBackupLastSuccess, success); err != nil {
			return err
		}
		if err := d.Settings.SetTx(ctx, tx, SettingBackupScheduleLast, now.Format(time.RFC3339)); err != nil {
			return err
		}
		return d.Settings.SetTx(ctx, tx, SettingBackupScheduleState, state)
	})
	if err != nil {
		return fail("record", err)
	}
	d.Audit.Record(ctx, "system.backup_scheduled", "backup", res.Stamp, map[string]any{"files": res.Count, "verified": true})
	var remoteErr error
	if remoteID := d.Settings.GetString(ctx, SettingBackupRemoteSchedule, ""); remoteID != "" {
		remoteErr = scheduledRemoteUpload(ctx, d, res, remoteID, client)
		if remoteErr != nil {
			state.Status, state.Stage = "warning", "remote"
		}
	}
	if err := renewLease(); err != nil {
		state.Status, state.Stage = "warning", "lease"
		return saveScheduledWarning(ctx, d, state, errors.Join(remoteErr, err))
	}
	pruned := 0
	if keep := backupRetain(ctx, d); keep > 0 {
		pruned, err = backup.PruneVerifiedKeeping(d.Config.DataDir, keep, res.Stamp)
		if err != nil {
			state.Status, state.Stage = "warning", "prune"
			d.Audit.Record(ctx, "system.backup_retention_failed", "backup", res.Stamp, map[string]string{"stage": "prune"})
			d.Log.Error("backup.schedule: retention failed", "err", err)
		}
	}
	if pruned > 0 {
		d.Audit.Record(ctx, "system.backup_retention", "backup", res.Stamp, map[string]any{"pruned": pruned, "retain": backupRetain(ctx, d)})
	}
	d.Log.Info("backup.schedule: verified", "stamp", res.Stamp, "pruned", pruned)
	if state.Status == "warning" {
		return saveScheduledWarning(ctx, d, state, errors.Join(remoteErr, err))
	}
	return err
}

type backupScheduleState struct {
	AttemptAt string `json:"attempt_at"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Stamp     string `json:"stamp,omitempty"`
}
type backupSuccess struct {
	At    string `json:"at"`
	Stamp string `json:"stamp"`
	Bytes int64  `json:"bytes"`
}

func verifiedBackup(res backup.Result, now time.Time) (backupSuccess, error) {
	report, err := backup.Inspect(res.Manifest)
	if err != nil {
		return backupSuccess{}, err
	}
	if report.Readiness == "blocked" || !report.IntegrityOK {
		return backupSuccess{}, fmt.Errorf("backup: verification failed")
	}
	manifest, err := backup.ReadManifest(res.Manifest)
	if err != nil {
		return backupSuccess{}, err
	}
	return backupSuccess{At: now.UTC().Format(time.RFC3339), Stamp: res.Stamp, Bytes: manifest.Database.Size + manifest.Archive.Size}, nil
}

func backupScheduleDue(ctx context.Context, d *kernel.Deps, now time.Time) bool {
	hours := backupScheduleHours(ctx, d)
	if hours < 1 {
		return false
	}
	last := backupScheduleLast(ctx, d)
	return last.IsZero() || !now.Before(last.Add(time.Duration(hours)*time.Hour))
}

func (state backupScheduleState) WarningKey() string {
	switch state.Stage {
	case "remote":
		return "remotebackup.warning"
	case "lease":
		return "remotebackup.lease_warning"
	default:
		return "admin.backups.alert_retention"
	}
}
