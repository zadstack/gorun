package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// GuardDelete exposes guardDelete to the external tests.
func GuardDelete(ctx context.Context, d *kernel.Deps, users auth.Service, targetID string) error {
	return guardDelete(ctx, d.DB, users, targetID)
}

// PruneJobsAt runs the jobs.prune cron body with an injected clock.
func PruneJobsAt(ctx context.Context, d *kernel.Deps, now time.Time) error {
	return pruneJobsAt(ctx, d, now)
}

// BackupScheduleAt runs the backup.schedule cron body with an injected clock.
func BackupScheduleAt(ctx context.Context, d *kernel.Deps, now time.Time) error {
	return backupScheduleAt(ctx, d, now)
}

// SetAnthropicURL points the Anthropic provider at u until the returned
// func restores it.
func SetAnthropicURL(u string) (restore func()) {
	old := anthropicURL
	anthropicURL = u
	return func() { anthropicURL = old }
}

func BackupScheduleWithClient(ctx context.Context, d *kernel.Deps, now time.Time, client *http.Client) error {
	return backupScheduleWithClient(ctx, d, now, client)
}

func BackupScheduleTick(ctx context.Context, d *kernel.Deps) error {
	return (&handlers{d: d}).backupScheduleTick(ctx)
}

func RunScheduledBackup(ctx context.Context, d *kernel.Deps, client *http.Client) error {
	return runScheduledBackup(ctx, d, client)
}
