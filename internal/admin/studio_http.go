package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

func (h *handlers) studioRoutes() {
	r := h.d.Router.Admin
	r.Get("/x/studio/{content}", h.studioList)
	r.Post("/x/studio/{content}/preview", h.studioPreview)
	r.Post("/x/studio/{content}/generate", h.studioGenerate)
	r.Post("/x/studio/{content}/{candidate}/action", h.studioAction)
	st := r.With(auth.RequirePerm(PermSettings))
	st.Get("/settings/studio", h.studioSettings)
	st.Post("/settings/studio", h.studioSettingsSave)
	st.Post("/settings/studio/discover", h.studioDiscover)
}
func studioRead(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errStudio
	}
	return nil
}
func (h *handlers) studioError(w http.ResponseWriter, status int) {
	h.aiJSON(w, status, map[string]string{"error": "Studio request unavailable. Check permissions, configuration and provider status."})
}
func (h *handlers) studioList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "content")
	if h.studioAccess(r.Context(), id) != nil {
		h.studioError(w, 403)
		return
	}
	config, e := h.studioConfig(r.Context())
	if e != nil {
		h.studioError(w, 500)
		return
	}
	type option struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	providers := []option{}
	if cfg, e := h.loadAIConfig(r.Context()); e == nil {
		providers = append(providers, option{"native-text", "Saved writing provider · generation not verified", "text"})
		if cfg.Provider == AIProviderOpenAI && config.ImageModel != "" {
			providers = append(providers, option{
				"native-image",
				"Saved image model · generation not verified: " + config.ImageModel,
				"image",
			})
		}
	}
	for _, b := range config.Bindings {
		providers = append(providers, option{b.ID, b.Name, b.Kind})
	}
	rows, e := h.d.DB.QueryContext(r.Context(), `SELECT document,status FROM admin_studio_candidates WHERE content_id=? AND user_id=? AND status!='removed' ORDER BY created_at DESC LIMIT 40`, id, auth.UserFromContext(r.Context()).ID)
	if e != nil {
		h.studioError(w, 500)
		return
	}
	defer rows.Close()
	items := []studioCandidate{}
	for rows.Next() {
		var raw []byte
		var status string
		if rows.Scan(&raw, &status) != nil {
			h.studioError(w, 500)
			return
		}
		c, e := studioDecode(raw)
		if e != nil {
			h.studioError(w, 500)
			return
		}
		c.Status = status
		if c.Status == "failed" && c.Error == "" {
			c.Error = "Interrupted generation. Check your provider before generating again."
		}
		c.Binding = nil
		items = append(items, c)
	}
	if rows.Err() != nil {
		h.studioError(w, 500)
		return
	}
	h.aiJSON(w, 200, map[string]any{"providers": providers, "presets": config.Presets, "items": items, "can_configure": auth.Can(r.Context(), PermSettings)})
}
func (h *handlers) studioPrepare(r *http.Request, in studioInput) (*studioBinding, map[string]any, error) {
	if h.studioAccess(r.Context(), chi.URLParam(r, "content")) != nil || !studioKind(in.Kind) || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > 8000 || len(in.Title) > 2000 || len(in.Text) > 100000 {
		return nil, nil, errStudio
	}
	if in.Kind != "text" && !auth.Can(r.Context(), "media.upload") {
		return nil, nil, errStudio
	}
	cfg, e := h.studioConfig(r.Context())
	if e != nil {
		return nil, nil, e
	}
	if in.Provider == "native-text" && in.Kind == "text" || in.Provider == "native-image" && in.Kind == "image" {
		c, e := h.loadAIConfig(r.Context())
		if e != nil || in.Provider == "native-image" && (cfg.ImageModel == "" || c.Provider != AIProviderOpenAI) {
			return nil, nil, errStudio
		}
		return nil, map[string]any{"provider": c.Provider, "endpoint": c.BaseURL, "model": func() string {
			if in.Kind == "image" {
				return cfg.ImageModel
			}
			return c.Model
		}(), "prompt": in.Prompt, "title": in.Title, "source": in.Text}, nil
	}
	for _, b := range cfg.Bindings {
		if b.ID == in.Provider && b.Kind == in.Kind {
			conn, e := h.loadConnection(r.Context(), b.ConnectionID)
			if e != nil || conn.Version != b.ConnectionVersion || conn.Endpoint != b.Endpoint {
				return nil, nil, errStudio
			}
			args, e := studioArguments(b.Arguments, in, "")
			return &b, map[string]any{"connection_id": b.ConnectionID, "connection_version": b.ConnectionVersion, "endpoint": b.Endpoint, "tool": b.Tool, "arguments": args}, e
		}
	}
	return nil, nil, errStudio
}
func (h *handlers) studioPreview(w http.ResponseWriter, r *http.Request) {
	var in studioInput
	if studioRead(w, r, &in) != nil {
		h.studioError(w, 400)
		return
	}
	_, args, e := h.studioPrepare(r, in)
	if e != nil {
		h.studioError(w, 400)
		return
	}
	h.aiJSON(w, 200, map[string]any{"arguments": args, "review_hash": studioReviewHash(args), "request_id": db.NewID()})
}
func (h *handlers) studioGenerate(w http.ResponseWriter, r *http.Request) {
	var in studioInput
	if studioRead(w, r, &in) != nil || !in.Reviewed || !connectionIDPattern.MatchString(in.RequestID) {
		h.studioError(w, 400)
		return
	}
	b, args, e := h.studioPrepare(r, in)
	if e != nil || in.ReviewHash != studioReviewHash(args) {
		h.studioError(w, 400)
		return
	}
	if !h.aiLimit.allow("studio:" + auth.UserFromContext(r.Context()).ID) {
		h.studioError(w, 429)
		return
	}
	c, e := h.studioCreate(r.Context(), chi.URLParam(r, "content"), in, b)
	if e != nil {
		h.studioError(w, 409)
		return
	}
	c.Binding = nil
	h.aiJSON(w, 202, c)
}
func (h *handlers) studioAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if studioRead(w, r, &in) != nil {
		h.studioError(w, 400)
		return
	}
	c, e := h.studioCandidate(r.Context(), chi.URLParam(r, "candidate"))
	if e != nil || h.studioAccess(r.Context(), chi.URLParam(r, "content")) != nil || c.ContentID != chi.URLParam(r, "content") || c.UserID != auth.UserFromContext(r.Context()).ID {
		h.studioError(w, 404)
		return
	}
	previous := c.Status
	switch in.Action {
	case "remove":
		if c.Status == "running" || c.Status == "queued" {
			h.studioError(w, 409)
			return
		}
		c.Status = "removed"
		c.Text = ""
		c.Input.Text = ""
		c.Input.Title = ""
		c.Input.Prompt = ""
		c.Binding = nil
		c.Native = nil
		c.RemoteJob = ""
	case "edit":
		if c.Status != "ready" || c.Input.Kind != "text" || len(in.Text) > 100000 {
			h.studioError(w, 409)
			return
		}
		c.Text = plainText.Sanitize(in.Text)
	case "check":
		if c.Status != "waiting" || c.Binding == nil || c.Binding.StatusTool == "" || !h.aiLimit.allow("studio:"+c.UserID) {
			h.studioError(w, 409)
			return
		}
		c.Status = "queued"
		raw, e := studioEncode(c)
		if e != nil {
			h.studioError(w, 500)
			return
		}
		tx, e := h.d.DB.BeginTx(r.Context(), nil)
		if e != nil {
			h.studioError(w, 500)
			return
		}
		defer tx.Rollback()
		res, e := tx.ExecContext(r.Context(), `UPDATE admin_studio_candidates SET status='queued',document=?,updated_at=? WHERE id=? AND status='waiting'`, raw, time.Now().UTC().Format(time.RFC3339), c.ID)
		if e != nil {
			h.studioError(w, 500)
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			h.studioError(w, 409)
			return
		}
		_, e = h.d.Jobs.EnqueueTx(r.Context(), tx, "studio.generate", map[string]any{"id": c.ID, "poll": true}, jobs.Idempotency("studio:check:"+c.ID+":"+time.Now().UTC().Format(time.RFC3339Nano)))
		if e != nil || tx.Commit() != nil {
			h.studioError(w, 500)
			return
		}
		h.aiJSON(w, 202, map[string]bool{"ok": true})
		return
	default:
		h.studioError(w, 400)
		return
	}
	if h.studioSave(r.Context(), c, previous) != nil {
		h.studioError(w, 409)
		return
	}
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}

// Separate context helper is used by discovery and never runs a generation tool.
func studioTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 90*time.Second)
}
