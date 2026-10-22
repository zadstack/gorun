package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const backupPolicyMaxBodyBytes = 4096

type backupPolicy struct {
	Hours             int    `json:"hours" doc:"0 disables scheduling; otherwise 1–168 hours"`
	Retain            int    `json:"retain" doc:"0 disables local cleanup; otherwise keep 1–100 verified pairs"`
	RemoteDestination string `json:"remote_destination" doc:"Empty disables uploads; otherwise must be the current active backup destination ID"`
}

type backupScheduleReport struct {
	backupPolicy
	ActiveRemoteDestination string              `json:"active_remote_destination"`
	LastLocal               backupSuccess       `json:"last_local"`
	LastAttempt             backupScheduleState `json:"last_attempt"`
	LastUpload              remoteBackupState   `json:"last_upload"`
	LastAcknowledgedUpload  remoteBackupState   `json:"last_acknowledged_upload"`
}

func saveBackupSchedule(ctx context.Context, d *kernel.Deps, policy backupPolicy) error {
	backupMu.Lock()
	defer backupMu.Unlock()
	return db.InTx(ctx, d.DB, func(tx *sql.Tx) error {
		if policy.RemoteDestination != "" {
			if err := validateScheduledDestination(ctx, tx, d, policy.RemoteDestination); err != nil {
				return err
			}
		}
		for _, entry := range []struct {
			key     string
			setting any
		}{{SettingBackupScheduleHours, policy.Hours}, {SettingBackupRetain, policy.Retain}, {SettingBackupRemoteSchedule, policy.RemoteDestination}} {
			if err := d.Settings.SetTx(ctx, tx, entry.key, entry.setting); err != nil {
				return err
			}
		}
		d.Audit.RecordTx(ctx, tx, "system.backup_schedule", "backup", "policy", map[string]any{"schedule_hours": policy.Hours, "retain": policy.Retain, "remote_destination": policy.RemoteDestination})
		return nil
	})
}

func (h *handlers) backupScheduleAPIOperations() {
	read := api.Op("GET", "/system/backups/schedule", "Read the saved backup schedule and separate local/upload outcomes; no external calls", []string{"system"}, []string{PermBackup}, nil, backupScheduleReport{}, h.apiBackupScheduleGet)
	write := api.Op("PUT", "/system/backups/schedule", "Replace the backup schedule; remote uploads require explicit selection of the active backup destination", []string{"system"}, []string{PermBackup}, backupPolicy{}, backupScheduleReport{}, h.apiBackupSchedulePut)
	write.Contract = &kernel.APIContract{MaxBodyBytes: backupPolicyMaxBodyBytes, Errors: []kernel.APIErrorResponse{{Status: 422, Code: "validation_failed", Description: "Invalid interval, retention or unavailable/changed backup destination"}}}
	h.d.API.Add(read)
	h.d.API.Add(write)
}

func (h *handlers) apiBackupScheduleGet(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermBackup) {
		return
	}
	page := backupsPage{}
	if err := h.backupRemoteView(r.Context(), &page); err != nil {
		api.Fail(w, r, err)
		return
	}
	h.backupScheduleView(r.Context(), &page)
	report := backupScheduleReport{backupPolicy: backupPolicy{Hours: page.Schedule.Hours, Retain: page.Schedule.Retain, RemoteDestination: page.Schedule.RemoteDestination}, ActiveRemoteDestination: page.RemoteID, LastLocal: page.LastSuccess, LastAttempt: page.Schedule.State, LastUpload: page.RemoteLast, LastAcknowledgedUpload: page.RemoteSuccess}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, report)
}

func (h *handlers) apiBackupSchedulePut(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermBackup) {
		return
	}
	var in struct {
		Hours             *int    `json:"hours"`
		Retain            *int    `json:"retain"`
		RemoteDestination *string `json:"remote_destination"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, backupPolicyMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		api.WriteError(w, 400, "bad_request", "Invalid backup policy JSON", nil)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		api.WriteError(w, 400, "bad_request", "Expected exactly one JSON object", nil)
		return
	}
	if in.Hours == nil || in.Retain == nil || in.RemoteDestination == nil || *in.Hours < 0 || *in.Hours > maxScheduleHours || *in.Retain < 0 || *in.Retain > maxRetainPairs {
		api.WriteError(w, 422, "validation_failed", "Provide hours (0–168), retain (0–100) and remote_destination (empty disables uploads)", nil)
		return
	}
	err := saveBackupSchedule(r.Context(), h.d, backupPolicy{Hours: *in.Hours, Retain: *in.Retain, RemoteDestination: *in.RemoteDestination})
	if errors.Is(err, errScheduledDestination) {
		api.WriteError(w, 422, "validation_failed", errScheduledDestination.Error(), nil)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	h.apiBackupScheduleGet(w, r)
}
