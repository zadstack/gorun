package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func canvasOperationFixture(t *testing.T, extra ...kernel.Module) (*handlers, context.Context, *canvasBoard, canvasPrepared) {
	t.Helper()
	modules := append([]kernel.Module{settingsmod.New(), auth.New()}, extra...)
	modules = append(modules, New())
	app, e := kernel.Boot(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, modules)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { app.Close() })
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	u, e := users.Create(context.Background(), "fixture@example.com", "Owner", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithUser(context.Background(), u)
	h := &handlers{d: app.Deps, users: users, keys: keys, aiLimit: newAILimiter()}
	_, key, e := keys.CreateKind(ctx, auth.KeyKindAgent, "Worker", u.ID, []string{"canvas.read", "canvas.append", "canvas.run", "content.view", "content.edit"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	id, e := h.canvasCreate(ctx, "Board")
	if e != nil {
		t.Fatal(e)
	}
	cards := []canvasCard{{ID: "11111111-1111-4111-8111-111111111111", Kind: "prompt", Body: "Summarize"}, {ID: "22222222-2222-4222-8222-222222222222", Kind: "agent", AgentID: key.ID}, {ID: "33333333-3333-4333-8333-333333333333", Kind: "schedule", Interval: "hourly"}}
	edges := []canvasEdge{{cards[0].ID, cards[1].ID}, {cards[2].ID, cards[1].ID}}
	if _, e = app.Deps.DB.Exec(`UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?`, canvasEncode(cards), canvasEncode(edges), id); e != nil {
		t.Fatal(e)
	}
	if _, e = app.Deps.DB.Exec(`INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,1,1,1,?)`, id, key.ID, canvasEncode([]string{cards[0].ID})); e != nil {
		t.Fatal(e)
	}
	b, e := h.canvasOwned(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	p, e := h.canvasPrepare(ctx, b, cards[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	return h, ctx, b, p
}
func TestCanvasScheduleSlotsAndRevocation(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	schedule := b.Cards[2].ID
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, e := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, b.ID, schedule, p.NodeID, p.GraphHash, past); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e := h.canvasTick(ctx); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("duplicate slot %d %v", n, e)
	}
	if _, e := h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_run=0 WHERE board_id=?`, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := h.d.DB.Exec(`UPDATE admin_canvas_schedules SET next_run_at=? WHERE board_id=?`, past, b.ID); e != nil {
		t.Fatal(e)
	}
	if e := h.canvasTick(ctx); e != nil {
		t.Fatal(e)
	}
	var enabled bool
	var status string
	if e := h.d.DB.QueryRow(`SELECT enabled,status FROM admin_canvas_schedules WHERE board_id=?`, b.ID).Scan(&enabled, &status); e != nil || enabled || status != "needs_review" {
		t.Fatal(enabled, status, e)
	}
	if e := h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&n); e != nil || n != 1 {
		t.Fatal("revoked scheduled run", n, e)
	}
}
func TestCanvasConsentIgnoresPositionButRejectsPromptChanges(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	b.Cards[0].X = 300
	b.Title = "Renamed"
	current, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil || current.GraphHash != p.GraphHash {
		t.Fatal("layout invalidated consent", e)
	}
	b.Cards[0].Body = "Different instruction"
	current, e = h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil || current.GraphHash == p.GraphHash {
		t.Fatal("prompt reused consent", e)
	}
}
func TestCanvasProviderUncertainIsNotRetried(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	// A run that already crossed the external boundary must not be dispatched again by queue retries.
	_, e := h.d.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES('uncertain',?,?,? ,?,'uncertain',?,?)`, b.ID, p.OwnerID, "once", p.NodeID, canvasEncode(p), time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]string{"id": "uncertain"})
	if e = h.canvasGenerateJob(ctx, raw); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id='uncertain'`).Scan(&status); e != nil || status != "uncertain" {
		t.Fatal(status, e)
	}
}

func TestCanvasAppendRechecksAtomicAuthority(t *testing.T) {
	for _, which := range []string{"nodes", "scope", "schedule", "owner", "cancelled"} {
		t.Run(which, func(t *testing.T) {
			h, ctx, b, p := canvasOperationFixture(t)
			p.ScheduleID = b.Cards[2].ID
			if _, e := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, b.ID, p.ScheduleID, p.NodeID, p.GraphHash, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); e != nil {
				t.Fatal(e)
			}
			run, e := h.canvasEnqueue(ctx, p, "checked")
			if e != nil {
				t.Fatal(e)
			}
			// Mutations happen after review/preflight and before the append transaction.
			switch which {
			case "cancelled":
				_, e = h.d.DB.Exec(`UPDATE admin_canvas_runs SET status='cancelled' WHERE id=?`, run.ID)
			case "nodes":
				_, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET node_ids='[]' WHERE board_id=?`, b.ID)
			case "scope":
				_, e = h.d.DB.Exec(`UPDATE api_keys SET scopes_json='["canvas.read","content.view"]' WHERE id=?`, p.KeyID)
			case "schedule":
				_, e = h.d.DB.Exec(`UPDATE admin_canvas_schedules SET enabled=0 WHERE board_id=?`, b.ID)
			case "owner":
				_, e = h.d.DB.Exec(`UPDATE users SET status='disabled' WHERE id=?`, p.OwnerID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = h.canvasAppend(ctx, run.ID, p, "Stale result", "api_key", "Agent", "waiting_agent"); e == nil {
				t.Fatal("stale authority accepted")
			}
			current, e := h.canvasOwned(ctx, b.ID)
			if e != nil || len(current.Cards) != 3 {
				t.Fatal("mutated despite rejection", e)
			}
		})
	}
}

func TestCanvasAsyncVideoOnlyPollsAndKeepsArtifactPrivate(t *testing.T) {
	h, ctx, b, _ := canvasOperationFixture(t)
	conn := connectionRecord{ID: "01M4QBA2J8ZQA5ARC42V1PCQHA", Version: "fixed", Name: "Video service", Kind: "mcp", Endpoint: "https://provider.example/mcp"}
	if e := h.d.Settings.Set(ctx, connectionPrefix+conn.ID, conn); e != nil {
		t.Fatal(e)
	}
	binding := studioBinding{ID: "video", Name: "Video", Kind: "video", ConnectionID: conn.ID, ConnectionVersion: conn.Version, Endpoint: conn.Endpoint, Tool: "create", Arguments: json.RawMessage(`{"prompt":"{{prompt}}"}`), URLPath: "structuredContent.url", JobPath: "structuredContent.job", StatusTool: "check", StatusArguments: json.RawMessage(`{"id":"{{job_id}}"}`), StatusPath: "structuredContent.status", ReadyValue: "ready", FailedValue: "failed"}
	if e := h.d.Settings.Set(ctx, "ai.studio", studioConfig{Bindings: []studioBinding{binding}}); e != nil {
		t.Fatal(e)
	}
	b.Cards[1].Kind = "provider"
	b.Cards[1].AgentID = ""
	b.Cards[1].ProviderID = "video"
	b.Cards[1].GenerationKind = "video"
	if _, e := h.d.DB.Exec(`UPDATE admin_canvases SET document_json=? WHERE id=?`, canvasEncode(b.Cards), b.ID); e != nil {
		t.Fatal(e)
	}
	p, e := h.canvasPrepare(ctx, b, b.Cards[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.d.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES('video-run',?,?,?,?,'queued',?,?)`, b.ID, p.OwnerID, "video", p.NodeID, canvasEncode(p), time.Now().UTC().Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	creates, polls := 0, 0
	fake := func(_ context.Context, c studioCandidate, poll bool) (studioOutput, error) {
		if !poll {
			creates++
			return studioOutput{JobID: "remote-job", Status: "working"}, nil
		}
		polls++
		if c.RemoteJob != "remote-job" {
			t.Fatal("lost remote job")
		}
		return studioOutput{Status: "ready", Bytes: []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}}, nil
	}
	raw := json.RawMessage(`{"id":"video-run"}`)
	if e = h.canvasGenerateWith(ctx, raw, fake); e != nil {
		t.Fatal(e)
	}
	if e = h.canvasGenerateWith(ctx, raw, fake); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id='video-run'`).Scan(&status); e != nil || status != "waiting" || creates != 1 || polls != 0 {
		t.Fatal("unexpected repeat", status, creates, polls, e)
	}
	if _, e = h.d.DB.Exec(`UPDATE admin_canvas_runs SET status='queued' WHERE id='video-run' AND status='waiting'`); e != nil {
		t.Fatal(e)
	}
	if e = h.canvasGenerateWith(ctx, json.RawMessage(`{"id":"video-run","poll":true}`), fake); e != nil {
		t.Fatal(e)
	}
	if e = h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id='video-run'`).Scan(&status); e != nil || status != "succeeded" || creates != 1 || polls != 1 {
		t.Fatal("async finish", status, creates, polls, e)
	}
	results, e := h.canvasResults(ctx, b.ID)
	if e != nil || results["video-run"]["kind"] != "video" || !strings.HasPrefix(results["video-run"]["url"], "/admin/canvases/") {
		t.Fatal(results, e)
	}
}

func TestCanvasPrivateImportRejectsActiveAndMalformedPayloads(t *testing.T) {
	h, ctx, _, p := canvasOperationFixture(t)
	p.Candidate.Input.Kind = "image"
	for _, raw := range [][]byte{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), []byte(`<html><script>alert(1)</script></html>`), append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, []byte("not a valid image")...)} {
		if e := h.canvasImportPrivate(ctx, &p, studioOutput{Bytes: raw}); e == nil {
			t.Fatal("unsafe payload imported")
		}
	}
}

func TestCanvasSourceChangesAndPrivateTypeBlockAppend(t *testing.T) {
	for _, which := range []string{"unchanged", "edit", "private"} {
		t.Run(which, func(t *testing.T) {
			h, ctx, b, p := canvasOperationFixture(t, content.New())
			cs, _ := h.contentService()
			it, e := cs.Create(ctx, content.Input{Type: "post", Title: "Selected article", Body: "Reviewed facts", Status: "draft", AuthorID: p.OwnerID})
			if e != nil {
				t.Fatal(e)
			}
			p.SourceTokens = map[string]string{it.ID: content.EditToken(it)}
			run, e := h.canvasEnqueue(ctx, p, "source-check")
			if e != nil {
				t.Fatal(e)
			}
			switch which {
			case "edit":
				_, e = h.d.DB.Exec(`UPDATE content SET body='Changed after review' WHERE id=?`, it.ID)
			case "private":
				_, e = h.d.DB.Exec(`UPDATE content_types SET public=0 WHERE slug='post'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			e = h.canvasAppend(ctx, run.ID, p, "Reviewed output", "api_key", "Agent", "waiting_agent")
			if which == "unchanged" && e != nil {
				t.Fatal("valid source blocked", e)
			}
			if which != "unchanged" && e == nil {
				t.Fatal("changed source accepted")
			}
			current, e := h.canvasOwned(ctx, b.ID)
			if e != nil {
				t.Fatal(e)
			}
			expected := 3
			if which == "unchanged" {
				expected = 4
			}
			if len(current.Cards) != expected {
				t.Fatal("unexpected append", len(current.Cards))
			}
		})
	}
}
