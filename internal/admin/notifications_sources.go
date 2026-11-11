package admin

import (
	"context"
	"database/sql"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// registerNotificationSources adds the operations items the admin module
// owns: failed background jobs, failed backups and failed updates, each from
// the last notifyWindow and behind the permission of its screen.
func (h *handlers) registerNotificationSources() {
	h.d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "admin.jobs", Permission: PermHealth, Load: h.notifyDeadJobs})
	h.d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "admin.backups", Permission: PermBackup, Load: h.notifyFailedBackups})
	h.d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "admin.updates", Permission: PermUpdate, Load: h.notifyFailedUpdates})
}

func notifySince() string { return db.FormatTime(time.Now().Add(-notifyWindow)) }

// collect scans (key, detail, at) rows into notifications built by mk.
func collect(rows *sql.Rows, err error, mk func(id, detail, at string) kernel.Notification) ([]kernel.Notification, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []kernel.Notification
	for rows.Next() {
		var id, detail, at string
		if err := rows.Scan(&id, &detail, &at); err != nil {
			return nil, err
		}
		out = append(out, mk(id, detail, at))
	}
	return out, rows.Err()
}

// notifyDeadJobs lists jobs that exhausted their retries.
func (h *handlers) notifyDeadJobs(ctx context.Context, limit int) ([]kernel.Notification, error) {
	rows, err := h.d.DB.QueryContext(ctx, `SELECT id, kind, updated_at FROM jobs WHERE status = 'dead' AND updated_at > ?
		ORDER BY updated_at DESC LIMIT ?`, notifySince(), limit)
	return collect(rows, err, func(id, kind, at string) kernel.Notification {
		return kernel.Notification{Key: "job.dead:" + id, TitleKey: "notifications.job_failed", Detail: kind,
			Href: "/admin/system/jobs?status=dead", At: at, Icon: "list-x", Tone: "danger"}
	})
}

// notifyFailedBackups reads the backup failure audit events (manual,
// scheduled and retention runs).
func (h *handlers) notifyFailedBackups(ctx context.Context, limit int) ([]kernel.Notification, error) {
	rows, err := h.d.DB.QueryContext(ctx, `SELECT id, action, at FROM audit_log
		WHERE action IN ('system.backup_failed', 'system.backup_schedule_failed', 'system.backup_retention_failed') AND at > ?
		ORDER BY at DESC LIMIT ?`, notifySince(), limit)
	return collect(rows, err, func(id, action, at string) kernel.Notification {
		title := "notifications.backup_failed"
		if action == "system.backup_retention_failed" {
			title = "notifications.backup_retention_failed"
		}
		return kernel.Notification{Key: "backup.failed:" + id, TitleKey: title, Href: "/admin/system/backups", At: at,
			Icon: "archive-x", Tone: "danger"}
	})
}

// notifyFailedUpdates lists staged updates that failed or could not roll back.
func (h *handlers) notifyFailedUpdates(ctx context.Context, limit int) ([]kernel.Notification, error) {
	rows, err := h.d.DB.QueryContext(ctx, `SELECT id, requested_version, updated_at FROM update_operations
		WHERE status IN ('failed', 'interrupted', 'rollback_failed') AND updated_at > ?
		ORDER BY updated_at DESC LIMIT ?`, notifySince(), limit)
	return collect(rows, err, func(id, version, at string) kernel.Notification {
		return kernel.Notification{Key: "update.failed:" + id, TitleKey: "notifications.update_failed", Detail: version,
			Href: "/admin/system/updates", At: at, Icon: "download", Tone: "danger"}
	})
}
