package admin

import (
	"context"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Watchdog limits for runs that stopped making progress. A stuck run would
// otherwise stay active forever and count against the board's run limit.
const (
	canvasRunningLimit      = 5 * time.Minute  // running without finishing: the worker was interrupted
	canvasQueuedLimit       = 15 * time.Minute // queued without starting: no worker picked it up
	canvasWaitingAgentLimit = 24 * time.Hour   // waiting for an external agent that never answered
)

// canvasWatchdog closes runs that stopped making progress and records why.
// It never retries: an interrupted external request may already have had an
// effect, so it becomes uncertain, and the owner decides whether to run again.
func (h *handlers) canvasWatchdog(ctx context.Context, now time.Time) error {
	before := func(d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }
	for _, sweep := range []struct{ from, to, reason, column, cutoff string }{
		{"running", "uncertain", "interrupted", "started_at", before(canvasRunningLimit)},
		{"queued", "failed", "not_started", "created_at", before(canvasQueuedLimit)},
		{"waiting_agent", "uncertain", "agent_timeout", "created_at", before(canvasWaitingAgentLimit)},
	} {
		if _, e := h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_runs SET status=?,stop_reason=?,finished_at=? WHERE status=? AND `+sweep.column+`<?`, sweep.to, sweep.reason, db.Now(), sweep.from, sweep.cutoff); e != nil {
			return e
		}
	}
	return nil
}

// canvasStopError is the run's error code for the history and the node's failure line.
func canvasStopError(reason string) string {
	switch reason {
	case "interrupted", "not_started", "agent_timeout":
		return "run_" + reason
	}
	return "run_stopped"
}
