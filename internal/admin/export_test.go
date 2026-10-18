package admin

import (
	"context"
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
