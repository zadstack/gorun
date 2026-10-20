package admin

import (
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func TestAgentRecipeUsesCurrentReviewedManagedPolicyWithoutGranting(t *testing.T) {
	f := newManagedFixture(t)
	p, e := recipeConfiguration(f.ctx, f.h.d.DB, f.prepared.OwnerID, f.prepared.KeyID)
	if e != nil {
		t.Fatal(e)
	}
	if p.Mode != "managed" || p.Model != "reviewed-model" || len(p.Tools) != 1 || p.Tools[0].Name != "lookup" || p.Tools[0].Connection != "Archive" {
		t.Fatal(p)
	}
	p.DraftID = db.NewID()
	p.Title = "Updates Researcher"
	p.Instructions = "Keep sources and dates explicit. Flag uncertainty."
	p.Context = "Approved synthetic evidence"
	p.Destination = "Private brief task"
	id, e := f.h.saveAgentRecipe(f.ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.h.canvasOwned(f.ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if f.modelCalls != 0 || f.toolCalls != 0 {
		t.Fatal("save contacted model/tool")
	}
	var n int
	f.h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_grants WHERE board_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("implicit grant")
	}
	if _, e = f.h.canvasPrepare(f.ctx, b, b.Cards[2].ID); e == nil {
		t.Fatal("review accepted without explicit grant")
	}
	// A separate fixture action represents the owner's existing grant control.
	_, e = f.h.d.DB.Exec(`INSERT INTO admin_canvas_grants(board_id,key_id,can_read,can_append,can_run,node_ids) VALUES(?,?,1,1,1,?)`, id, p.KeyID, canvasEncode([]string{b.Cards[0].ID, b.Cards[1].ID}))
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := f.h.canvasPrepare(f.ctx, b, b.Cards[2].ID)
	if e != nil {
		t.Fatal(e)
	}
	if prepared.ManagedTools == nil || len(prepared.ManagedTools.Allow) != 1 || prepared.ManagedTools.Allow[0].Alias != f.alias {
		t.Fatal("changed reviewed tool policy")
	}
	if f.modelCalls != 0 || f.toolCalls != 0 {
		t.Fatal("review contacted provider")
	}
	f.board = b
	f.prepared = prepared
	run := f.run(t)
	if f.status(t, run) != "waiting_tool" || f.modelCalls != 1 || f.toolCalls != 0 {
		t.Fatal("tool ran without approval")
	}
	f.approve(t, run)
	f.work(t, run)
	if f.status(t, run) != "succeeded" || f.modelCalls != 2 || f.toolCalls != 1 {
		t.Fatal("reviewed protocol did not complete")
	}
	f.h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvas_provenance WHERE run_id=?`, run).Scan(&n)
	if n != 1 {
		t.Fatal("private result missing")
	}
	// The saved recipe can be edited using the ordinary owned canvas document;
	// structural template extraction still omits agent IDs and all free text.
	c, _, e := canvasTemplateDocument(b)
	if e != nil {
		t.Fatal(e)
	}
	for _, card := range c {
		if card.AgentID != "" || card.Body != "" {
			t.Fatal("template copied recipe authority/context")
		}
	}
}

func TestAgentRecipeSaveRechecksTransactionAndLimit(t *testing.T) {
	f := newManagedFixture(t)
	p, e := recipeConfiguration(f.ctx, f.h.d.DB, f.prepared.OwnerID, f.prepared.KeyID)
	if e != nil {
		t.Fatal(e)
	}
	p.Title = "Brief"
	p.Instructions = "Keep sources explicit"
	p.Destination = "Brief task"
	p.DraftID = db.NewID()
	// Exact same-transaction guard rejects a snapshot after a profile change.
	c, e := f.h.loadConnection(f.ctx, f.prepared.AgentModel.ConnectionID)
	if e != nil {
		t.Fatal(e)
	}
	c.Name = "Renamed without version bump"
	f.h.d.Settings.Set(f.ctx, connectionPrefix+c.ID, c)
	if _, e = f.h.saveAgentRecipe(f.ctx, p); e == nil {
		t.Fatal("stale config saved")
	}
	p, e = recipeConfiguration(f.ctx, f.h.d.DB, f.prepared.OwnerID, f.prepared.KeyID)
	if e != nil {
		t.Fatal(e)
	}
	p.Title = "Brief"
	p.Instructions = "Keep sources explicit"
	p.Destination = "Brief task"
	p.DraftID = db.NewID()
	for i := 1; i < 100; i++ {
		if _, e = f.h.canvasCreate(f.ctx, "Existing"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.h.saveAgentRecipe(f.ctx, p); e == nil {
		t.Fatal("exceeded canvas cap")
	}
	var n int
	f.h.d.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE owner_id=?`, auth.UserFromContext(f.ctx).ID).Scan(&n)
	if n != 100 {
		t.Fatal(n)
	}
}
