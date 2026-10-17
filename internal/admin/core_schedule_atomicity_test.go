package admin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func coreScheduledAgentFixture(t *testing.T) (*handlers, context.Context, *canvasBoard, canvasPrepared) {
	t.Helper()
	app, err := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, []kernel.Module{settingsmod.New(), auth.New(), New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	owner, err := users.Create(context.Background(), "schedule-fixture@example.invalid", "Owner", "long-password-value", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithUser(context.Background(), owner)
	h := &handlers{d: app.Deps, users: users, keys: keys, aiLimit: newAILimiter()}
	_, key, err := keys.CreateKind(ctx, auth.KeyKindAgent, "Scheduled worker", owner.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.canvasCreate(ctx, "Scheduled board")
	if err != nil {
		t.Fatal(err)
	}
	cards := []canvasCard{{ID: "11111111-1111-4111-8111-111111111111", Kind: "prompt", Body: "Summarize fixture"}, {ID: "22222222-2222-4222-8222-222222222222", Kind: "agent", AgentID: key.ID}, {ID: "33333333-3333-4333-8333-333333333333", Kind: "schedule", Interval: "hourly"}}
	edges := []canvasEdge{{cards[0].ID, cards[1].ID}, {cards[2].ID, cards[1].ID}}
	if _, err := app.Deps.DB.Exec(`UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?`, canvasEncode(cards), canvasEncode(edges), id); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.Exec(`INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,1,1,1,?)`, id, key.ID, canvasEncode([]string{cards[0].ID})); err != nil {
		t.Fatal(err)
	}
	connection := connectionRecord{ID: db.NewID(), Version: db.NewID(), Kind: "ai", Name: "Fixture only", Endpoint: "https://provider.example.invalid/v1", Provider: "openai", Model: "fixture-model"}
	if err := app.Deps.Settings.Set(ctx, connectionPrefix+connection.ID, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := h.saveAgentModel(ctx, key.ID, "", agentModelConfig{Mode: "managed", ConnectionID: connection.ID, ConnectionVersion: connection.Version, Model: "fixture-model"}); err != nil {
		t.Fatal(err)
	}
	board, err := h.canvasOwned(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.canvasPrepare(ctx, board, cards[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	return h, ctx, board, prepared
}

func TestScheduledCanvasConcurrentTicksQueueOneSlot(t *testing.T) {
	h, ctx, board, prepared := coreScheduledAgentFixture(t)
	scheduleID := board.Cards[2].ID
	due := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, board.ID, scheduleID, prepared.NodeID, prepared.GraphHash, due); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			results <- h.canvasTick(ctx)
		}()
	}
	close(start)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{`SELECT count(*) FROM admin_canvas_runs`, `SELECT count(*) FROM jobs WHERE kind='canvas.generate'`, `SELECT count(*) FROM audit_log WHERE action='canvas.run'`} {
		var count int
		if err := h.d.DB.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("concurrent ticks did not produce exactly one slot artifact: %d", count)
		}
	}
	var request, next string
	if err := h.d.DB.QueryRow(`SELECT request_id FROM admin_canvas_runs`).Scan(&request); err != nil {
		t.Fatal(err)
	}
	if err := h.d.DB.QueryRow(`SELECT next_run_at FROM admin_canvas_schedules`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339, next)
	if err != nil || !parsed.After(time.Now()) || parsed.Format(time.RFC3339) != parsed.UTC().Format(time.RFC3339) || request != "schedule:"+scheduleID+":"+due {
		t.Fatalf("slot identity or UTC next-run changed: request=%s next=%s err=%v", request, next, err)
	}
}

func TestScheduledCanvasUncertainResultRequiresFreshReview(t *testing.T) {
	h, ctx, board, prepared := coreScheduledAgentFixture(t)
	due := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, board.ID, board.Cards[2].ID, prepared.NodeID, prepared.GraphHash, due); err != nil {
		t.Fatal(err)
	}
	if err := h.canvasTick(ctx); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := h.d.DB.QueryRow(`SELECT id FROM admin_canvas_runs`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	complete := func(context.Context, aiConfig, string, string) (string, error) {
		calls++
		return "", errors.New("fixture provider accepted then disconnected")
	}
	for range 2 {
		if err := h.canvasGenerateUsing(ctx, raw, nil, complete); err != nil {
			t.Fatal(err)
		}
	}
	var status, scheduleStatus string
	var enabled bool
	if err := h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := h.d.DB.QueryRow(`SELECT enabled,status FROM admin_canvas_schedules WHERE board_id=?`, board.ID).Scan(&enabled, &scheduleStatus); err != nil {
		t.Fatal(err)
	}
	var appended int
	if err := h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, id).Scan(&appended); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || status != "uncertain" || enabled || scheduleStatus != "needs_review" || appended != 0 {
		t.Fatalf("uncertain provider request was repeated or authorized a result: calls=%d status=%s enabled=%v schedule=%s appended=%d", calls, status, enabled, scheduleStatus, appended)
	}
}

func TestScheduledCanvasEnqueueFailurePreservesDueSlot(t *testing.T) {
	h, ctx, board, prepared := coreScheduledAgentFixture(t)
	scheduleID := board.Cards[2].ID
	due := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, board.ID, scheduleID, prepared.NodeID, prepared.GraphHash, due); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.Exec(`CREATE TRIGGER fixture_reject_canvas_job BEFORE INSERT ON jobs WHEN NEW.kind='canvas.generate' BEGIN SELECT RAISE(ABORT,'fixture_enqueue_failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := h.canvasTick(ctx); err == nil {
		t.Fatal("injected enqueue failure was not reported")
	}
	var next string
	if err := h.d.DB.QueryRow(`SELECT next_run_at FROM admin_canvas_schedules WHERE board_id=? AND node_id=?`, board.ID, scheduleID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != due {
		t.Errorf("failed enqueue consumed the due slot: got %q want %q", next, due)
	}
	for _, query := range []string{`SELECT count(*) FROM admin_canvas_runs`, `SELECT count(*) FROM jobs WHERE kind='canvas.generate'`, `SELECT count(*) FROM audit_log WHERE action='canvas.run'`} {
		var count int
		if err := h.d.DB.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("failed enqueue left partial artifacts: %d", count)
		}
	}
	if _, err := h.d.DB.Exec(`DROP TRIGGER fixture_reject_canvas_job`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := h.canvasTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var request, status string
	var runs, queuedJobs int
	if err := h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := h.d.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='canvas.generate' AND status='queued'`).Scan(&queuedJobs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || queuedJobs != 1 {
		t.Fatalf("retry did not enqueue exactly one original slot: runs=%d jobs=%d", runs, queuedJobs)
	}
	if err := h.d.DB.QueryRow(`SELECT request_id,status FROM admin_canvas_runs`).Scan(&request, &status); err != nil {
		t.Fatal(err)
	}
	if request != "schedule:"+scheduleID+":"+due || status != "queued" {
		t.Errorf("retry lost original slot identity: %s %s", request, status)
	}
}
