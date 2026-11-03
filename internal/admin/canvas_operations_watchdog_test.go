package admin

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func insertCanvasRun(t *testing.T, h *handlers, board, owner, node, id, status string, created, started time.Time) {
	t.Helper()
	at := func(v time.Time) string {
		if v.IsZero() {
			return ""
		}
		return v.UTC().Format(time.RFC3339)
	}
	if _, e := h.d.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at,started_at) VALUES(?,?,?,?,?,?,'{}',?,?)`, id, board, owner, "req-"+id, node, status, at(created), at(started)); e != nil {
		t.Fatal(e)
	}
}

func TestCanvasWatchdogStopsStuckRuns(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	now := time.Now()
	runs := []struct {
		id, status, wantStatus, wantReason string
		created, started                   time.Time
	}{
		{"run-stale", "running", "uncertain", "interrupted", now.Add(-10 * time.Minute), now.Add(-6 * time.Minute)},
		{"run-live", "running", "running", "", now.Add(-2 * time.Minute), now.Add(-time.Minute)},
		{"queued-stale", "queued", "failed", "not_started", now.Add(-16 * time.Minute), time.Time{}},
		{"queued-live", "queued", "queued", "", now.Add(-time.Minute), time.Time{}},
		{"agent-stale", "waiting_agent", "uncertain", "agent_timeout", now.Add(-25 * time.Hour), time.Time{}},
		{"agent-live", "waiting_agent", "waiting_agent", "", now.Add(-time.Hour), time.Time{}},
		{"tool-wait", "waiting_tool", "waiting_tool", "", now.Add(-48 * time.Hour), time.Time{}},
	}
	for _, r := range runs {
		insertCanvasRun(t, h, b.ID, p.OwnerID, p.NodeID, r.id, r.status, r.created, r.started)
	}
	if e := h.canvasWatchdog(ctx, now); e != nil {
		t.Fatal(e)
	}
	for _, r := range runs {
		var status, reason, finished string
		if e := h.d.DB.QueryRow(`SELECT status,stop_reason,finished_at FROM admin_canvas_runs WHERE id=?`, r.id).Scan(&status, &reason, &finished); e != nil {
			t.Fatal(e)
		}
		if status != r.wantStatus || reason != r.wantReason || (r.wantReason != "") != (finished != "") {
			t.Fatalf("%s: got %s/%q finished=%q, want %s/%q", r.id, status, reason, finished, r.wantStatus, r.wantReason)
		}
	}
	listed, e := h.canvasRuns(ctx, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	errs := map[string]string{}
	for _, r := range listed {
		errs[r.ID] = r.Error
	}
	if errs["run-stale"] != "run_interrupted" || errs["queued-stale"] != "run_not_started" || errs["agent-stale"] != "run_agent_timeout" || errs["agent-live"] != "" {
		t.Fatalf("run errors: %v", errs)
	}
}

func TestCanvasStopErrorFallsBack(t *testing.T) {
	if canvasStopError("") != "run_stopped" || canvasStopError("bogus") != "run_stopped" || canvasStopError("agent_timeout") != "run_agent_timeout" {
		t.Fatal("stop error mapping")
	}
}

// Stuck runs count as active against the board's run limit; the watchdog frees the board.
func TestCanvasWatchdogUnblocksBoardLimit(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	old := time.Now().Add(-25 * time.Hour)
	for i := range 20 {
		insertCanvasRun(t, h, b.ID, p.OwnerID, p.NodeID, fmt.Sprintf("stuck-%02d", i), "waiting_agent", old, time.Time{})
	}
	if _, e := h.canvasEnqueue(ctx, p, "fresh-request"); !errors.Is(e, errCanvasLimit) {
		t.Fatalf("expected the limit while runs are stuck, got %v", e)
	}
	if e := h.canvasTick(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := h.canvasEnqueue(ctx, p, "fresh-request"); e != nil {
		t.Fatalf("board still blocked after the watchdog: %v", e)
	}
}
