package admin

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"testing"
)

func agentModelFixture(t *testing.T) (*handlers, context.Context, *canvasBoard, canvasPrepared, connectionRecord) {
	t.Helper()
	h, ctx, b, p := canvasOperationFixture(t)
	c := connectionRecord{ID: db.NewID(), Version: db.NewID(), Kind: "ai", Name: "Named AI", Endpoint: "https://api.openai.com/v1", Provider: "openai", Model: "default"}
	if e := h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	return h, ctx, b, p, c
}
func TestAgentModelSettingsCASIsolation(t *testing.T) {
	h, ctx, _, p, c := agentModelFixture(t)
	initial, e := h.loadAgentModel(ctx, p.KeyID)
	if e != nil || initial.Mode != "external" || initial.Version != "" {
		t.Fatal(initial, e)
	}
	want := agentModelConfig{Mode: "managed", ConnectionID: c.ID, ConnectionVersion: c.Version, Model: "model-one"}
	got, e := h.saveAgentModel(ctx, p.KeyID, "", want)
	if e != nil || got.Version == "" {
		t.Fatal(got, e)
	}
	if _, e = h.saveAgentModel(ctx, p.KeyID, "", want); !errors.Is(e, errAgentModelConflict) {
		t.Fatal("stale CAS", e)
	}
	_, k, e := h.keys.CreateKind(ctx, auth.KeyKindAgent, "Second", auth.UserFromContext(ctx).ID, []string{"canvas.run"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	want.Model = "model-two"
	if _, e = h.saveAgentModel(ctx, k.ID, "", want); e != nil {
		t.Fatal(e)
	}
	a, _ := h.loadAgentModel(ctx, p.KeyID)
	z, _ := h.loadAgentModel(ctx, k.ID)
	if a.Model != "model-one" || z.Model != "model-two" {
		t.Fatal(a, z)
	}
	c.Version = db.NewID()
	if e = h.d.Settings.Set(ctx, connectionPrefix+c.ID, c); e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentModel(ctx, p.KeyID, got.Version, want); !errors.Is(e, errAgentModelConflict) {
		t.Fatal("changed connection", e)
	}
	other, e := h.users.Create(ctx, "other-agent@example.com", "Other", "long-password-value", "admin")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentModel(auth.WithUser(ctx, other), p.KeyID, got.Version, agentModelConfig{Mode: "external"}); !errors.Is(e, errAgentModelForbidden) {
		t.Fatal("other owner", e)
	}
	if _, e = h.d.DB.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, db.Now(), p.KeyID); e != nil {
		t.Fatal(e)
	}
	if _, e = h.saveAgentModel(ctx, p.KeyID, got.Version, agentModelConfig{Mode: "external"}); !errors.Is(e, errAgentModelForbidden) {
		t.Fatal("revoked key", e)
	}
}
