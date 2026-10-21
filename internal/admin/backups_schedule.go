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
	"github.com/zadstack/gorun/internal/update"
)

// Local backup schedule / retention settings (System → Backups).
const (
	SettingBackupScheduleHours = "backup.schedule_hours"
	SettingBackupRetain        = "backup.retain"
	SettingBackupScheduleLast  = "backup.schedule.last"
	SettingBackupScheduleState = "backup.schedule.state"
	SettingBackupLastSuccess   = "backup.local.last_success"
)

const (
	maxScheduleHours = 168 // one week
	maxRetainPairs   = 100
)

// backupSchedulePolicy is the schedule form state on the backups page.
type backupSchedulePolicy struct {
	Hours    int
	Retain   int
	Last     string
	NextHint string
	Enabled  bool
	State    backupScheduleState
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
	backupMu.Lock()
	err := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if err := h.d.Settings.SetTx(ctx, tx, SettingBackupScheduleHours, hours); err != nil {
			return err
		}
		if err := h.d.Settings.SetTx(ctx, tx, SettingBackupRetain, retain); err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, "system.backup_schedule", "backup", "policy", map[string]any{"schedule_hours": hours, "retain": retain})
		return nil
	})
	backupMu.Unlock()
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.backups.schedule_saved")
	http.Redirect(w, r, "/admin/system/backups", http.StatusSeeOther)
}

// backupScheduleTick is the hourly backup.schedule cron body.
func (h *handlers) backupScheduleTick(ctx context.Context) error {
	return backupScheduleAt(ctx, h.d, time.Now().UTC())
}

// backupScheduleAt runs one scheduled local backup when due (injected clock for tests).
func backupScheduleAt(ctx context.Context, d *kernel.Deps, now time.Time) error {
	now = now.UTC()
	hours := backupScheduleHours(ctx, d)
	if hours < 1 {
		return nil
	}
	last := backupScheduleLast(ctx, d)
	if !last.IsZero() && now.Before(last.Add(time.Duration(hours)*time.Hour)) {
		return nil
	}
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
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	// Re-check policy and last success under the shared lock: an operator may
	// have disabled the schedule while this tick waited for another backup.
	hours = backupScheduleHours(ctx, d)
	if hours < 1 {
		return nil
	}
	// A concurrent tick must not double-create.
	last = backupScheduleLast(ctx, d)
	if !last.IsZero() && now.Before(last.Add(time.Duration(hours)*time.Hour)) {
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
	pruned := 0
	if keep := backupRetain(ctx, d); keep > 0 {
		pruned, err = backup.PruneVerified(d.Config.DataDir, keep)
		if err != nil {
			state.Status, state.Stage = "warning", "prune"
			stateErr := d.Settings.Set(ctx, SettingBackupScheduleState, state)
			err = errors.Join(err, stateErr)
			d.Audit.Record(ctx, "system.backup_retention_failed", "backup", res.Stamp, map[string]string{"stage": "prune"})
			d.Log.Error("backup.schedule: retention failed", "err", err)
		}
	}
	d.Audit.Record(ctx, "system.backup_scheduled", "backup", res.Stamp, map[string]any{"files": res.Count, "pruned": pruned, "retain": backupRetain(ctx, d), "verified": true})
	d.Log.Info("backup.schedule: verified", "stamp", res.Stamp, "pruned", pruned)
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
