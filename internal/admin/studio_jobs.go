package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func (h *handlers) studioGenerateJob(ctx context.Context, raw json.RawMessage) error {
	var p struct {
		ID   string `json:"id"`
		Poll bool   `json:"poll"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return errStudio
	}
	c, e := h.studioCandidate(ctx, p.ID)
	if e != nil {
		return e
	}
	if c.Status != "queued" {
		return nil
	}
	c.Status = "running"
	if e = h.studioSave(ctx, c, "queued"); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	u, e := h.users.Get(ctx, c.UserID)
	if e != nil || u.Status != "active" {
		return h.studioFailed(ctx, c)
	}
	ctx = auth.WithUser(ctx, u)
	if h.studioAccess(ctx, c.ContentID) != nil || c.Input.Kind != "text" && !auth.Can(ctx, "media.upload") {
		return h.studioFailed(ctx, c)
	}
	out, e := h.studioProvider(ctx, c, p.Poll)
	if e == nil {
		e = h.studioImport(ctx, &c, out)
	}
	if e != nil {
		return h.studioFailed(ctx, c)
	}
	c.Error = ""
	return h.studioSave(ctx, c, "running")
}
func (h *handlers) studioFailed(ctx context.Context, c studioCandidate) error {
	// A provider may have accepted a charge before its response was lost. Never auto-repeat creation.
	c.Status = "failed"
	c.Error = "Generation or import failed. Check the provider before explicitly generating again."
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return h.studioSave(saveCtx, c, "running")
}

func (h *handlers) studioRecover(ctx context.Context) error {
	// An interrupted create is uncertain, not a safe retry. Keep its record for operator review.
	_, e := h.d.DB.ExecContext(ctx, `UPDATE admin_studio_candidates SET status='failed',updated_at=? WHERE status='running' AND updated_at<?`, time.Now().UTC().Format(time.RFC3339), time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339))
	if e != nil {
		return e
	}
	if _, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent); ok {
		_, e = h.d.DB.ExecContext(ctx, `DELETE FROM admin_studio_candidates WHERE NOT EXISTS(SELECT 1 FROM content WHERE content.id=admin_studio_candidates.content_id)`)
	}
	return e
}
