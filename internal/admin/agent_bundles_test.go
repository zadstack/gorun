package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func bundleFixture(t *testing.T, h *handlers, ctx context.Context) agentBundle {
	t.Helper()
	b := agentBundle{Skills: []agentSkill{{Name: "Editorial policy", Instructions: "Cite the supplied evidence.", ResourceName: "Style guide", ResourceText: "Keep the output private."}}}
	for _, name := range []string{"Research MCP", "Archive MCP"} {
		c := connectionRecord{ID: db.NewID(), Version: db.NewID(), Kind: "mcp", Name: name, Endpoint: "https://mcp.example/capability/PATH_SECRET", Secret: "ENCRYPTED_SECRET", TestStatus: "pass", TestedAt: db.Now()}
		if e := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
			t.Fatal(e)
		}
		b.MCP = append(b.MCP, agentMCPReference{ID: c.ID, Version: c.Version})
	}
	return b
}
func TestAgentBundleEffectiveManagedExecutionAndPrivateResult(t *testing.T) {
	h, ctx, b, p, c := agentModelFixture(t)
	bundle, e := h.saveAgentBundle(ctx, p.KeyID, "", bundleFixture(t, h, ctx))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "bundle-model"}); e != nil {
		t.Fatal(e)
	}
	prepared, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(prepared.Prompt, "Cite the supplied evidence.") || !strings.Contains(prepared.Prompt, "Keep the output private.") || !strings.Contains(prepared.Prompt, "Summarize") || prepared.AgentBundleVersion != bundle.Version || prepared.Candidate.Input.Prompt != prepared.Prompt {
		t.Fatal(prepared.Prompt)
	}
	run, e := h.canvasEnqueue(ctx, prepared, "bundle-managed")
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	if e = h.canvasGenerateUsing(ctx, json.RawMessage(`{"id":"`+run.ID+`"}`), nil, func(_ context.Context, cfg aiConfig, system, user string) (string, error) {
		calls++
		if cfg.Model != "bundle-model" || !strings.Contains(system+user, "Cite the supplied evidence.") || !strings.Contains(system+user, "Keep the output private.") {
			t.Error("skills not applied to actual model input")
		}
		return "Private skill-aware result", nil
	}); e != nil {
		t.Fatal(e)
	}
	var status string
	h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, run.ID).Scan(&status)
	if calls != 1 || status != "succeeded" {
		t.Fatal(calls, status)
	}
	var n int
	h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, run.ID).Scan(&n)
	if n != 1 {
		t.Fatal("missing private result", n)
	}
}
func TestAgentBundleIsolationLimitsProfilesAndCAS(t *testing.T) {
	h, ctx, b, p, _ := agentModelFixture(t)
	want := bundleFixture(t, h, ctx)
	saved, e := h.saveAgentBundle(ctx, p.KeyID, "", want)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentBundle(ctx, p.KeyID, "", want); !errors.Is(e, errAgentModelConflict) {
		t.Fatal("stale save", e)
	}
	other, e := h.users.Create(ctx, "other-bundle@example.test", "Other", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentBundle(auth.WithUser(ctx, other), p.KeyID, saved.Version, want); e == nil {
		t.Fatal("foreign owner save")
	}
	if _, e = currentAgentBundle(ctx, h.d.DB, other.ID, p.KeyID); e == nil {
		t.Fatal("foreign owner load")
	}
	oversized := want
	oversized.Skills = []agentSkill{}
	for i := 0; i < 4; i++ {
		oversized.Skills = append(oversized.Skills, agentSkill{Name: strings.Repeat("x", i+1), Instructions: strings.Repeat("أ", 1900)})
	}
	if _, e = h.saveAgentBundle(ctx, p.KeyID, saved.Version, oversized); e == nil {
		t.Fatal("combined byte limit bypass")
	}
	changed := want
	changed.MCP = append([]agentMCPReference{}, want.MCP...)
	changed.MCP[0].Version = db.NewID()
	if _, e = h.saveAgentBundle(ctx, p.KeyID, saved.Version, changed); e == nil {
		t.Fatal("unpinned profile accepted")
	}
	var c connectionRecord
	if e = h.d.Settings.Get(ctx, connectionPrefix+want.MCP[0].ID, &c); e != nil {
		t.Fatal(e)
	}
	c.Kind = "ai"
	c.Provider = "openai"
	c.Model = "m"
	if e = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	if _, e = h.canvasPrepare(ctx, b, p.NodeID); e == nil {
		t.Fatal("non-MCP reference used")
	}
	if _, e = h.saveAgentBundle(ctx, p.KeyID, saved.Version, want); e == nil {
		t.Fatal("non-MCP profile attached")
	}
}
func TestAgentBundleFullPromptCapAndAuthorityLoss(t *testing.T) {
	h, ctx, b, p, _ := agentModelFixture(t)
	want := agentBundle{Skills: []agentSkill{{Name: "One", Instructions: strings.Repeat("x", 3900)}, {Name: "Two", Instructions: strings.Repeat("x", 3900)}, {Name: "Three", Instructions: strings.Repeat("x", 3900)}}}
	if _, e := h.saveAgentBundle(ctx, p.KeyID, "", want); e != nil {
		t.Fatal(e)
	}
	b.Cards[0].Body = strings.Repeat("y", 5000)
	if _, e := h.canvasPrepare(ctx, b, p.NodeID); e == nil {
		t.Fatal("combined final prompt limit bypass")
	}
	b.Cards[0].Body = "Summarize"
	if _, e := h.canvasPrepare(ctx, b, p.NodeID); e != nil {
		t.Fatal(e)
	}
	if _, e := h.d.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, p.OwnerID); e != nil {
		t.Fatal(e)
	}
	if _, e := h.canvasPrepare(ctx, b, p.NodeID); e == nil {
		t.Fatal("management authority loss ignored")
	}
}
func TestAgentBundleChangedAndRestoredTextRejectsQueuedAndInflight(t *testing.T) {
	for _, stage := range []string{"before", "during"} {
		t.Run(stage, func(t *testing.T) {
			h, ctx, b, p, c := agentModelFixture(t)
			initial := agentBundle{Skills: []agentSkill{{Name: "One", Instructions: "Original skill"}}}
			saved, e := h.saveAgentBundle(ctx, p.KeyID, "", initial)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "m"}); e != nil {
				t.Fatal(e)
			}
			p, e = h.canvasPrepare(ctx, b, p.NodeID)
			if e != nil {
				t.Fatal(e)
			}
			run, e := h.canvasEnqueue(ctx, p, "version-check")
			if e != nil {
				t.Fatal(e)
			}
			mutate := func() {
				next := initial
				next.Skills = []agentSkill{{Name: "One", Instructions: "Changed skill"}}
				next, e = h.saveAgentBundle(ctx, p.KeyID, saved.Version, next)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = h.saveAgentBundle(ctx, p.KeyID, next.Version, initial); e != nil {
					t.Fatal(e)
				}
			}
			if stage == "before" {
				mutate()
			}
			calls := 0
			if e = h.canvasGenerateUsing(ctx, json.RawMessage(`{"id":"`+run.ID+`"}`), nil, func(context.Context, aiConfig, string, string) (string, error) {
				calls++
				if stage == "during" {
					mutate()
				}
				return "must not append", nil
			}); e != nil {
				t.Fatal(e)
			}
			var status string
			h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, run.ID).Scan(&status)
			if status != "failed" || stage == "before" && calls != 0 || stage == "during" && calls != 1 {
				t.Fatal(status, calls)
			}
			var n int
			h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, run.ID).Scan(&n)
			if n != 0 {
				t.Fatal("stale result appended")
			}
		})
	}
}
func TestAgentBundleRetiresInboundSchedulesAndLegacySnapshot(t *testing.T) {
	h, ctx, b, p, id, secret := inboundFixture(t, true)
	if _, e := h.d.DB.Exec(`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'hourly',?,1,?,'enabled')`, b.ID, b.Cards[2].ID, p.NodeID, p.GraphHash, db.Now()); e != nil {
		t.Fatal(e)
	}
	saved, e := h.saveAgentBundle(ctx, p.KeyID, "", agentBundle{Skills: []agentSkill{{Name: "Policy", Instructions: "Current policy"}}})
	if e != nil {
		t.Fatal(e)
	}
	var enabled, revision int
	var state string
	h.d.DB.QueryRow(`SELECT enabled,revision,status FROM admin_canvas_inbound WHERE id=?`, id).Scan(&enabled, &revision, &state)
	if enabled != 0 || revision <= 1 || state != "needs_review" {
		t.Fatal(enabled, revision, state)
	}
	h.d.DB.QueryRow(`SELECT enabled,status FROM admin_canvas_schedules WHERE board_id=?`, b.ID).Scan(&enabled, &state)
	if enabled != 0 || state != "needs_review" {
		t.Fatal(enabled, state)
	}
	if result := inboundRequest(h, id, secret, "bundle_retired_delivery", `{"command":"summarize"}`, time.Now(), true); result.Code != 409 {
		t.Fatal(result.Code)
	}
	legacy := p
	legacy.AgentModel = nil
	legacy.InboundID = ""
	legacy.ScheduleID = ""
	if _, e = h.canvasEnqueue(ctx, legacy, "legacy-no-model"); e == nil {
		t.Fatal("legacy snapshot ignored new bundle")
	}
	current, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil || current.AgentBundleVersion != saved.Version {
		t.Fatal(e)
	}
	if e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error { _, e := agentModelAuthorityTx(ctx, tx, current); return e }); e != nil {
		t.Fatal(e)
	}
	if _, e = h.d.DB.Exec(`DELETE FROM settings WHERE key=?`, agentBundlePrefix+p.KeyID); e != nil {
		t.Fatal(e)
	}
	if e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error { _, e := agentModelAuthorityTx(ctx, tx, current); return e }); e == nil {
		t.Fatal("deleted bundle authority accepted")
	}
}

func TestAgentBundleExternalWaitingAppendRejectsRestoredInstructions(t *testing.T) {
	h, ctx, b, p := canvasOperationFixture(t)
	initial := agentBundle{Skills: []agentSkill{{Name: "Policy", Instructions: "Keep private"}}}
	one, e := h.saveAgentBundle(ctx, p.KeyID, "", initial)
	if e != nil {
		t.Fatal(e)
	}
	p, e = h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	run, e := h.canvasEnqueue(ctx, p, "external-bundle")
	if e != nil || run.Status != "waiting_agent" {
		t.Fatal(run, e)
	}
	two, e := h.saveAgentBundle(ctx, p.KeyID, one.Version, agentBundle{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentBundle(ctx, p.KeyID, two.Version, initial); e != nil {
		t.Fatal(e)
	}
	// Legacy documents can lack a model pointer. Key authority still binds them.
	p.AgentModel = nil
	if e = h.canvasAppend(ctx, run.ID, p, "Must not appear", "api_key", "Worker", "waiting_agent"); e == nil {
		t.Fatal("stale external append accepted")
	}
	var n int
	h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, run.ID).Scan(&n)
	if n != 0 {
		t.Fatal("stale output visible")
	}
}
