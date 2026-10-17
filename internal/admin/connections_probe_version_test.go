package admin

import (
	"errors"
	"testing"
)

func TestConnectionProbePreservesManagedAssignment(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			h, ctx, b, p, c := agentModelFixture(t)
			cfg, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "chosen-model"})
			if e != nil {
				t.Fatal(e)
			}
			before, e := h.canvasPrepare(ctx, b, p.NodeID)
			if e != nil {
				t.Fatal(e)
			}
			var probeErr error
			if failed {
				probeErr = errors.New("upstream unavailable")
			}
			probe := recordConnectionProbe(c, connectionProbeResult{Count: 2}, probeErr)
			if e = h.writeConnection(ctx, probe, c.Version, "connections.test", false); e != nil {
				t.Fatal(e)
			}
			after, e := h.canvasPrepare(ctx, b, p.NodeID)
			if e != nil {
				t.Fatalf("probe invalidated assignment: %v", e)
			}
			if probe.Version != c.Version || before.ReviewHash != after.ReviewHash {
				t.Fatal("probe changed configuration consent")
			}
			runtime, e := h.agentModelRuntime(ctx, after)
			if e != nil || runtime.Model != cfg.Model {
				t.Fatal("assigned model no longer usable", e)
			}
			saved, e := h.loadConnection(ctx, c.ID)
			if e != nil || saved.TestedAt == "" {
				t.Fatal("probe metadata missing", e)
			}
			if failed && saved.TestStatus != "fail" || !failed && saved.TestStatus != "pass" {
				t.Fatal(saved.TestStatus)
			}
		})
	}
}

func TestDelayedConnectionProbeRejectsConfigurationEditOrRemoval(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "edit"
		if remove {
			name = "remove"
		}
		t.Run(name, func(t *testing.T) {
			h, ctx, b, p, c := agentModelFixture(t)
			if _, e := h.saveAgentModel(ctx, p.KeyID, "", agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "chosen-model"}); e != nil {
				t.Fatal(e)
			}
			delayed := recordConnectionProbe(c, connectionProbeResult{Count: 2}, nil)
			edited := changedConnection(c)
			edited.Model = "edited-default"
			if e := h.writeConnection(ctx, edited, c.Version, "connections.save", remove); e != nil {
				t.Fatal(e)
			}
			if e := h.writeConnection(ctx, delayed, c.Version, "connections.test", false); !errors.Is(e, errConnectionConflict) {
				t.Fatal("delayed probe overwrote changed configuration", e)
			}
			if _, e := h.canvasPrepare(ctx, b, p.NodeID); e == nil {
				t.Fatal("changed/removed connection accepted old agent assignment")
			}
			if !remove {
				saved, e := h.loadConnection(ctx, c.ID)
				if e != nil || saved.Version != edited.Version || saved.Model != edited.Model || saved.TestedAt != "" {
					t.Fatal("edit overwritten", e)
				}
			}
		})
	}
}
