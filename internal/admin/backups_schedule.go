package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// Local backup schedule / retention settings (System → Backups).
const (
	SettingBackupScheduleHours = "backup.schedule_hours"
	SettingBackupRetain        = "backup.retain"
	SettingBackupScheduleLast  = "backup.schedule.last"
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
	page.Schedule = p
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
	if err := h.d.Settings.Set(ctx, SettingBackupScheduleHours, hours); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.d.Settings.Set(ctx, SettingBackupRetain, retain); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "system.backup_schedule", "backup", "policy", map[string]any{
		"schedule_hours": hours,
		"retain":         retain,
	})
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
	backupMu.Lock()
	defer backupMu.Unlock()
	// Re-check under the create lock so a concurrent tick does not double-create.
	last = backupScheduleLast(ctx, d)
	if !last.IsZero() && now.Before(last.Add(time.Duration(hours)*time.Hour)) {
		return nil
	}
	res, err := backup.Create(d.Config.DataDir, d.Config.DBPath(), now)
	if err != nil {
		d.Log.Error("backup.schedule: create failed", "err", err)
		return err
	}
	pruned := 0
	if keep := backupRetain(ctx, d); keep >= 1 {
		pruned, err = backup.Prune(d.Config.DataDir, keep)
		if err != nil {
			d.Log.Error("backup.schedule: prune failed", "err", err, "stamp", res.Stamp)
			// Still record last success for create; prune can retry next tick.
		}
	}
	if err := d.Settings.Set(ctx, SettingBackupScheduleLast, now.Format(time.RFC3339)); err != nil {
		return err
	}
	d.Audit.Record(ctx, "system.backup_scheduled", "backup", res.Stamp, map[string]any{
		"files":  res.Count,
		"pruned": pruned,
		"retain": backupRetain(ctx, d),
	})
	d.Log.Info("backup.schedule: created", "stamp", res.Stamp, "pruned", pruned)
	return nil
}
