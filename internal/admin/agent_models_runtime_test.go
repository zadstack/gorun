package admin

import (
	"context"
	"encoding/json"
	"github.com/zadstack/gorun/internal/db"
	"testing"
)

func TestManagedAgentReviewAndRuntime(t *testing.T) {
	h, ctx, b, p, c := agentModelFixture(t)
	cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "independent-model"})
	if e != nil {
		t.Fatal(e)
	}
	managed, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if managed.ReviewHash == p.ReviewHash || managed.AgentModel == nil || managed.AgentModel.Model != cfg.Model {
		t.Fatal("model not reviewed")
	}
	run, e := h.canvasEnqueue(ctx, managed, "managed-request")
	if e != nil || run.Status != "queued" {
		t.Fatal(run, e)
	}
	calls := 0
	e = h.canvasGenerateUsing(ctx, json.RawMessage(`{"id":"`+run.ID+`"}`), nil, func(ctx context.Context, c aiConfig, system, user string) (string, error) {
		calls++
		if c.Model != "independent-model" || c.BaseURL != "https://api.openai.com/v1" {
			t.Error("wrong model", c.Model)
		}
		return "Private factual result", nil
	})
	if e != nil || calls != 1 {
		t.Fatal(calls, e)
	}
	var status string
	if e = h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, run.ID).Scan(&status); e != nil || status != "succeeded" {
		t.Fatal(status, e)
	}
}
func TestManagedAgentAuthorityChanges(t *testing.T) {
	for _, stage := range []string{"before", "during"} {
		for _, change := range []string{"key", "grant", "read", "scopes", "role", "model", "connection"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				h, ctx, b, p, c := agentModelFixture(t)
				cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "m"})
				if e != nil {
					t.Fatal(e)
				}
				p, e = h.canvasPrepare(ctx, b, p.NodeID)
				if e != nil {
					t.Fatal(e)
				}
				run, e := h.canvasEnqueue(ctx, p, "request")
				if e != nil {
					t.Fatal(e)
				}
				mutate := func() {
					t.Helper()
					var e error
					switch change {
					case "key":
						_, e = h.d.DB.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, db.Now(), p.KeyID)
					case "grant":
						_, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_append=0 WHERE key_id=?`, p.KeyID)
					case "read":
						_, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_read=0 WHERE key_id=?`, p.KeyID)
					case "scopes":
						_, e = h.d.DB.Exec(`UPDATE api_keys SET scopes_json='["canvas.run"]' WHERE id=?`, p.KeyID)
					case "role":
						_, e = h.d.DB.Exec(`DELETE FROM role_permissions WHERE role='admin'`)
					case "model":
						cfg.Version = db.NewID()
						cfg.Model = "changed"
						e = h.d.Settings.Set(ctx, agentModelPrefix+p.KeyID, cfg)
					case "connection":
						c.Version = db.NewID()
						e = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c)
					}
					if e != nil {
						t.Fatal(e)
					}
				}
				if stage == "before" {
					mutate()
				}
				calls := 0
				e = h.canvasGenerateUsing(ctx, json.RawMessage(`{"id":"`+run.ID+`"}`), nil, func(context.Context, aiConfig, string, string) (string, error) {
					calls++
					if stage == "during" {
						mutate()
					}
					return "must not append", nil
				})
				if e != nil {
					t.Fatal(e)
				}
				if stage == "before" && calls != 0 {
					t.Fatal("outgoing call after revocation")
				}
				var status string
				h.d.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, run.ID).Scan(&status)
				if status != "failed" {
					t.Fatal(status)
				}
				var n int
				h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, run.ID).Scan(&n)
				if n != 0 {
					t.Fatal("appended revoked result")
				}
			})
		}
	}
}

func TestManagedAgentAppendAtomicModelAndReadGrant(t *testing.T) {
	for _, change := range []string{"model", "connection", "read"} {
		t.Run(change, func(t *testing.T) {
			h, ctx, b, p, c := agentModelFixture(t)
			cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "m"})
			if e != nil {
				t.Fatal(e)
			}
			p, e = h.canvasPrepare(ctx, b, p.NodeID)
			if e != nil {
				t.Fatal(e)
			}
			run, e := h.canvasEnqueue(ctx, p, "atomic")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = h.d.DB.Exec(`UPDATE admin_canvas_runs SET status='running' WHERE id=?`, run.ID); e != nil {
				t.Fatal(e)
			}
			switch change {
			case "model":
				cfg.Version = db.NewID()
				e = h.d.Settings.Set(ctx, agentModelPrefix+p.KeyID, cfg)
			case "connection":
				c.Version = db.NewID()
				e = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c)
			case "read":
				_, e = h.d.DB.Exec(`UPDATE admin_canvas_grants SET can_read=0 WHERE key_id=?`, p.KeyID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = h.canvasAppend(ctx, run.ID, p, "obsolete result", "provider", "Managed", "running"); e == nil {
				t.Fatal("accepted stale authority")
			}
		})
	}
}
func TestExternalAgentPreferenceRemainsDelegated(t *testing.T) {
	h, ctx, b, p, c := agentModelFixture(t)
	cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "external", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "preferred"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil || p.Provider["model"] != cfg.Model {
		t.Fatal(p.Provider, e)
	}
	run, e := h.canvasEnqueue(ctx, p, "external")
	if e != nil || run.Status != "waiting_agent" {
		t.Fatal(run, e)
	}
	var jobs int
	h.d.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='canvas.generate'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("external dispatched")
	}
}

func TestAgentModelChangeInvalidatesScheduleAndDefaultAppend(t *testing.T) {
	h, ctx, b, p, c := agentModelFixture(t)
	run, e := h.canvasEnqueue(ctx, p, "external-default")
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "external", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "preference"})
	if e != nil {
		t.Fatal(e)
	}
	if e = h.canvasAppend(ctx, run.ID, p, "stale result", "api_key", "Worker", "waiting_agent"); e == nil {
		t.Fatal("default config changed inside append boundary")
	}
	current, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if current.GraphHash == p.GraphHash {
		t.Fatal("schedule consent unchanged")
	}
	cfg.Mode = "managed"
	if _, e = h.saveAgentModel(ctx, p.KeyID, cfg.Version, cfg); e != nil {
		t.Fatal(e)
	}
	managed, e := h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if managed.GraphHash == current.GraphHash {
		t.Fatal("mode switch did not invalidate schedule consent")
	}
}

func TestProviderOnlyConsentKeepsHistoricalHash(t *testing.T) {
	h, ctx, b, _, _ := agentModelFixture(t)
	for k, v := range map[string]string{"ai.provider": "openai", "ai.base_url": "https://api.openai.com/v1", "ai.model": "native-model"} {
		if e := h.d.Settings.Set(ctx, k, v); e != nil {
			t.Fatal(e)
		}
	}
	b.Cards[1].Kind = "provider"
	b.Cards[1].AgentID = ""
	b.Cards[1].ProviderID = "native-text"
	p, e := h.canvasPrepare(ctx, b, b.Cards[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	graph := append([]canvasCard{}, b.Cards...)
	for i := range graph {
		graph[i].X = 0
		graph[i].Y = 0
	}
	old := studioReviewHash(map[string]any{"nodes": graph, "provider": p.Provider, "binding": p.Candidate.Binding, "native": p.Candidate.Native, "prompt": p.Prompt})
	if p.GraphHash != old || p.AgentModel != nil {
		t.Fatal("existing provider consent invalidated")
	}
}
func TestManagedAgentEnqueueRechecksReviewedAssignment(t *testing.T) {
	h, ctx, b, p, c := agentModelFixture(t)
	cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "m"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = h.canvasPrepare(ctx, b, p.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Model = "changed"
	if _, e = h.saveAgentModel(ctx, p.KeyID, cfg.Version, cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = h.canvasEnqueue(ctx, p, "stale-assignment"); e == nil {
		t.Fatal("accepted stale model configuration")
	}
}
