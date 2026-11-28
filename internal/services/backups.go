package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

// The backup schedule settings of System → Backups (internal/admin/backups_schedule.go).
const (
	settingBackupScheduleHours  = "backup.schedule_hours"              // JSON int, 0 = no schedule
	settingBackupRetain         = "backup.retain"                      // JSON int, 0 = keep every pair
	settingBackupRemoteSchedule = "backup.schedule.remote_destination" // JSON string, "" = local only
	settingBackupLastSuccess    = "backup.local.last_success"          // JSON {at, stamp, bytes}
	settingStorageActiveBackup  = "storage.active.backup"              // JSON string, a destination id
)

// The admin's own ranges (maxScheduleHours, maxRetainPairs).
const (
	maxBackupEveryHours = 168
	maxBackupRetain     = 100
)

// destinationLocal is the destination value meaning no upload.
const destinationLocal = "local"

const errBackupRange = "admin.backups.schedule_invalid"

// backupsService uploads with client (nil: the guarded storage client).
type backupsService struct {
	d      *kernel.Deps
	client *http.Client
}

// Backups is the scheduled local backup over the System → Backups
// settings: enabled and every_hours over backup.schedule_hours (the
// schedule is on while it is 1 or more), retain over backup.retain and
// destination ("local", or the active backup destination's id) over
// backup.schedule.remote_destination. It has no secret.
func Backups(d *kernel.Deps) Service { return backupsService{d: d} }

func (backupsService) Name() string        { return "backups" }
func (backupsService) Providers() []string { return []string{providerGoRun} }
func (backupsService) Fields() []Field {
	return []Field{
		{Name: "enabled", Type: "bool"},
		{Name: "every_hours", Type: "int"},
		{Name: "retain", Type: "int"},
		{Name: "destination", Type: "string"},
	}
}
func (backupsService) Secret(string) string { return "" }

func (s backupsService) intSetting(ctx context.Context, key string, max int) int {
	var n int
	if err := s.d.Settings.Get(ctx, key, &n); err != nil || n < 1 {
		return 0
	}
	return min(n, max)
}

func (s backupsService) Status(ctx context.Context) (Status, error) {
	hours := s.intSetting(ctx, settingBackupScheduleHours, maxBackupEveryHours)
	dest := s.d.Settings.GetString(ctx, settingBackupRemoteSchedule, "")
	st := Status{Name: "backups", Provider: providerGoRun, State: StateOff, Missing: []string{}, Source: "settings",
		Fields: map[string]any{
			"enabled":     hours >= 1,
			"every_hours": hours,
			"retain":      s.intSetting(ctx, settingBackupRetain, maxBackupRetain),
			"destination": destinationLocal,
		}}
	if dest != "" {
		st.Fields["destination"] = dest
	}
	var last struct {
		At string `json:"at"`
	}
	if err := s.d.Settings.Get(ctx, settingBackupLastSuccess, &last); err == nil && last.At != "" {
		st.Fields["last_success"] = last.At
	}
	if hours >= 1 {
		st.State = StateConfigured
		// The scheduler pauses uploads to a destination that is no
		// longer the active backup one (scheduledBackupDestination).
		if dest != "" && s.d.Settings.GetString(ctx, settingStorageActiveBackup, "") != dest {
			st.State, st.Missing = StateIncomplete, []string{"destination"}
		}
	}
	return st, nil
}

func (s backupsService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, _ *string) (map[string]string, error) {
	errs := map[string]string{}
	hours := s.intSetting(ctx, settingBackupScheduleHours, maxBackupEveryHours)
	retain := s.intSetting(ctx, settingBackupRetain, maxBackupRetain)
	dest := s.d.Settings.GetString(ctx, settingBackupRemoteSchedule, "")

	var enabled *bool
	if v, ok := values["enabled"]; ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			errs["enabled"] = errInvalid
		} else {
			enabled = &b
		}
	}
	every := 0
	if v, ok := values["every_hours"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > maxBackupEveryHours {
			errs["every_hours"] = errBackupRange
		} else {
			every = n
		}
	}
	if v, ok := values["retain"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 || n > maxBackupRetain {
			errs["retain"] = errBackupRange
		} else {
			retain = n
		}
	}
	if v, ok := values["destination"]; ok {
		v = strings.TrimSpace(v)
		if v == destinationLocal || v == "" {
			dest = ""
		} else if err := backupDestinationOK(ctx, tx, s.d, v); err != nil {
			if !errors.Is(err, errBadDestination) {
				return nil, err
			}
			errs["destination"] = "remotebackup.invalid_destination"
		} else {
			dest = v
		}
	}
	// The schedule is on while backup.schedule_hours is 1 or more, so
	// the interval is the switch: every_hours alone turns it on, and
	// enabled=true needs an interval when none is stored.
	switch {
	case enabled != nil && !*enabled:
		hours = 0
	case every > 0:
		hours = every
	case enabled != nil && *enabled && hours < 1 && errs["every_hours"] == "":
		errs["every_hours"] = errRequired
	}
	if len(errs) > 0 {
		return errs, nil
	}
	for _, kv := range []struct {
		key string
		v   any
	}{{settingBackupScheduleHours, hours}, {settingBackupRetain, retain}, {settingBackupRemoteSchedule, dest}} {
		if err := s.d.Settings.SetTx(ctx, tx, kv.key, kv.v); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

var errBadDestination = errors.New("services: not the active backup destination")

// backupDestinationOK is the admin's validateScheduledDestination: id is
// the active backup destination, stored with purpose backup and
// credentials that still open with this site's key.
func backupDestinationOK(ctx context.Context, tx *sql.Tx, d *kernel.Deps, id string) error {
	read := func(key string, out any) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, key).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBadDestination
			}
			return err
		}
		if json.Unmarshal([]byte(raw), out) != nil {
			return errBadDestination
		}
		return nil
	}
	var active string
	if err := read(settingStorageActiveBackup, &active); err != nil {
		return err
	}
	if active != id {
		return errBadDestination
	}
	var dest media.Destination
	if err := read(media.DestinationPrefix+id, &dest); err != nil {
		return err
	}
	if dest.ID != id || dest.Purpose != "backup" {
		return errBadDestination
	}
	if _, err := media.DestinationConfig(dest, d.Config.SecretKey); err != nil {
		return errBadDestination
	}
	return nil
}

func (backupsService) Test(context.Context, string) (TestResult, error) {
	return TestResult{OK: true, Message: NoTest}, nil
}

// Run takes a verified backup now and uploads it to the configured
// destination (as a scheduled run does, honouring retain and recording
// last_success); the schedule's own clock is untouched. A failed upload
// is a result that did not pass, the local pair kept.
func (s backupsService) Run(ctx context.Context, via string) (RunResult, error) {
	run, err := admin.RunBackupNow(ctx, s.d, s.client, via)
	res := RunResult{At: run.At, Stamp: run.Stamp, Bytes: run.Bytes, DurationMS: run.Duration.Milliseconds(),
		Destination: run.Destination, Remote: run.Remote, Objects: run.Objects, Pruned: run.Pruned, Warning: run.Warning}
	switch {
	case errors.Is(err, admin.ErrBackupRemote):
		res.Message = "backup " + run.Stamp + " saved locally; the upload to " + run.Destination + " ended " + run.Remote
		return res, nil
	case errors.Is(err, admin.ErrBackupBusy):
		return RunResult{}, ErrBackupBusy
	case err != nil:
		return RunResult{}, err
	}
	res.OK = true
	res.Message = "backup " + run.Stamp + " saved locally"
	if run.Remote != "" {
		res.Message += " and uploaded to " + run.Destination
	}
	return res, nil
}

// ErrBackupBusy is a run refused because the data directory is busy (an
// update, a restore or another backup).
var ErrBackupBusy = admin.ErrBackupBusy

// Off stops the schedule; the retention, the destination and every backup
// already made stay.
func (s backupsService) Off(ctx context.Context, tx *sql.Tx) error {
	return s.d.Settings.SetTx(ctx, tx, settingBackupScheduleHours, 0)
}
