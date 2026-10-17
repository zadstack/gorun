package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/zadstack/gorun/internal/settings"
	"slices"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

type studioNative struct{ Provider, BaseURL, Model string }
type studioCandidate struct {
	Native    *studioNative  `json:"native,omitempty"`
	ID        string         `json:"id"`
	ContentID string         `json:"content_id"`
	UserID    string         `json:"-"`
	Status    string         `json:"status"`
	Input     studioInput    `json:"input"`
	Binding   *studioBinding `json:"binding,omitempty"`
	Text      string         `json:"text"`
	MediaID   string         `json:"media_id"`
	MediaURL  string         `json:"media_url"`
	RemoteJob string         `json:"-"`
	Error     string         `json:"error"`
	CreatedAt string         `json:"created_at"`
}

// Persist a private document separately from the public candidate projection.
type studioDocument struct {
	Candidate studioCandidate `json:"candidate"`
	UserID    string          `json:"user_id"`
	RemoteJob string          `json:"remote_job"`
}

func studioEncode(c studioCandidate) ([]byte, error) {
	return json.Marshal(studioDocument{c, c.UserID, c.RemoteJob})
}
func studioDecode(raw []byte) (studioCandidate, error) {
	var d studioDocument
	e := json.Unmarshal(raw, &d)
	d.Candidate.UserID = d.UserID
	d.Candidate.RemoteJob = d.RemoteJob
	return d.Candidate, e
}
func (h *handlers) studioConfig(ctx context.Context) (studioConfig, error) {
	var c studioConfig
	e := h.d.Settings.Get(ctx, "ai.studio", &c)
	if errors.Is(e, settings.ErrNotFound) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	return c, validateStudioConfig(c)
}
func (h *handlers) studioAccess(ctx context.Context, id string) error {
	u := auth.UserFromContext(ctx)
	if u == nil || !auth.Can(ctx, "content.edit") || !connectionIDPattern.MatchString(id) {
		return errStudio
	}
	svc, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent)
	if !ok {
		return errStudio
	}
	it, e := svc.Get(ctx, id)
	if e != nil || it.Status == "trash" {
		return errStudio
	}
	if (u.Role == "author" || u.Role == "contributor") && it.AuthorID != u.ID {
		return errStudio
	}
	typ, e := svc.GetType(ctx, it.Type)
	if e != nil || !slices.Contains(typ.Supports, "body") {
		return errStudio
	}
	return nil
}
func (h *handlers) studioCandidate(ctx context.Context, id string) (studioCandidate, error) {
	var raw []byte
	var status string
	e := h.d.DB.QueryRowContext(ctx, `SELECT document,status FROM admin_studio_candidates WHERE id=?`, id).Scan(&raw, &status)
	if e != nil {
		return studioCandidate{}, e
	}
	c, e := studioDecode(raw)
	c.Status = status
	return c, e
}
func (h *handlers) studioSave(ctx context.Context, c studioCandidate, previous string) error {
	raw, e := studioEncode(c)
	if e != nil {
		return e
	}
	res, e := h.d.DB.ExecContext(ctx, `UPDATE admin_studio_candidates SET document=?,status=?,updated_at=? WHERE id=? AND status=?`, raw, c.Status, time.Now().UTC().Format(time.RFC3339), c.ID, previous)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil || n != 1 {
		return errStudio
	}
	return nil
}
func (h *handlers) studioCreate(ctx context.Context, contentID string, in studioInput, b *studioBinding) (studioCandidate, error) {
	c := studioCandidate{ID: db.NewID(), ContentID: contentID, UserID: auth.UserFromContext(ctx).ID, Status: "queued", Input: in, Binding: b, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if b == nil {
		cfg, e := h.loadAIConfig(ctx)
		if e != nil {
			return c, e
		}
		model := cfg.Model
		if in.Kind == "image" {
			sc, e := h.studioConfig(ctx)
			if e != nil {
				return c, e
			}
			model = sc.ImageModel
		}
		c.Native = &studioNative{cfg.Provider, cfg.BaseURL, model}
		if in.ReviewHash != studioReviewHash(map[string]any{"provider": cfg.Provider, "endpoint": cfg.BaseURL, "model": model, "prompt": in.Prompt, "title": in.Title, "source": in.Text}) {
			return c, errStudio
		}
	}
	raw, e := studioEncode(c)
	if e != nil {
		return c, e
	}
	tx, e := h.d.DB.BeginTx(ctx, nil)
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	var prior []byte
	var priorStatus string
	e = tx.QueryRowContext(ctx, `SELECT document,status FROM admin_studio_candidates WHERE user_id=? AND request_key=?`, c.UserID, in.RequestID).Scan(&prior, &priorStatus)
	if e == nil {
		old, e := studioDecode(prior)
		if e != nil || old.ContentID != contentID || old.Input != in {
			return c, errStudio
		}
		old.Status = priorStatus
		return old, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return c, e
	}
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_studio_candidates WHERE content_id=? AND user_id=? AND status!='removed'`, contentID, c.UserID).Scan(&n); e != nil {
		return c, e
	}
	if n >= 40 {
		return c, errStudio
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO admin_studio_candidates(id,content_id,user_id,request_key,status,document,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, c.ID, c.ContentID, c.UserID, in.RequestID, c.Status, raw, c.CreatedAt, c.CreatedAt)
	if e != nil {
		return c, e
	}
	_, e = h.d.Jobs.EnqueueTx(ctx, tx, "studio.generate", map[string]string{"id": c.ID}, jobs.Idempotency("studio:"+c.ID))
	if e != nil {
		return c, e
	}
	h.d.Audit.RecordTx(ctx, tx, "studio.generate", "content", contentID, map[string]any{"candidate_id": c.ID, "kind": in.Kind, "provider": in.Provider})
	return c, tx.Commit()
}
