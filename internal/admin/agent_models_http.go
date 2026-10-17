package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

type agentModelPage struct {
	ID, Name, Error string
	Config          agentModelConfig
	Profiles        []connectionView
}

func (h *handlers) agentModelPage(ctx context.Context, id string) (*agentModelPage, error) {
	if !auth.Can(ctx, PermAPIKeys) || !auth.Can(ctx, "settings.manage") {
		return nil, errAgentModelForbidden
	}
	key, e := h.canvasAgentKey(ctx, id)
	if e != nil {
		return nil, errAgentModelForbidden
	}
	cfg, e := h.loadAgentModel(ctx, id)
	if e != nil {
		return nil, e
	}
	rows, e := h.listConnections(ctx)
	if e != nil {
		return nil, e
	}
	page := &agentModelPage{ID: id, Name: key.Name, Config: cfg}
	for _, c := range rows {
		if c.Kind == "ai" {
			page.Profiles = append(page.Profiles, viewConnection(c))
		}
	}
	return page, nil
}

func (h *handlers) agentModelSave(w http.ResponseWriter, r *http.Request) {
	h.agentModelSaveWithClient(w, r, connectionHTTPClient())
}
func (h *handlers) agentModelSaveWithClient(w http.ResponseWriter, r *http.Request, client *http.Client) {
	id := urlID(r)
	page, e := h.agentModelPage(r.Context(), id)
	if e != nil {
		http.Error(w, http.StatusText(403), 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		http.Error(w, http.StatusText(400), 400)
		return
	}
	cfg := agentModelConfig{Mode: r.PostForm.Get("mode"), ConnectionID: r.PostForm.Get("connection_id"), ConnectionVersion: r.PostForm.Get("connection_version"), Model: strings.TrimSpace(r.PostForm.Get("model")), Version: r.PostForm.Get("version")}
	fail := func(status int, message string) {
		if status >= 500 {
			status = http.StatusUnprocessableEntity
		}
		page.Config = cfg
		page.Error = message
		h.renderKeys(w, r, status, keysPage{Form: newKeyForm(), AgentModel: page})
	}
	if cfg.Version != page.Config.Version {
		fail(409, "agentmodels.conflict")
		return
	}
	if cfg.Mode != "external" && cfg.Mode != "managed" {
		fail(422, "agentmodels.invalid")
		return
	}
	if cfg.ConnectionID == "" {
		if cfg.Mode != "external" {
			fail(422, "agentmodels.invalid")
			return
		}
		cfg.ConnectionVersion = ""
		cfg.Model = ""
	} else {
		c, err := h.loadConnection(r.Context(), cfg.ConnectionID)
		if err != nil || c.Kind != "ai" {
			fail(422, "agentmodels.invalid")
			return
		}
		if c.Version != cfg.ConnectionVersion {
			fail(409, "agentmodels.conflict")
			return
		}
		secret, err := h.connectionSecrets(c)
		if err != nil {
			fail(422, "agentmodels.credential_unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		rows, err := discoverAIModels(ctx, client, c, secret)
		cancel()
		if err != nil {
			code, status := connectionModelFailure(err)
			fail(status, "connectionmodels."+code)
			return
		}
		if !slices.ContainsFunc(rows, func(m connectionModel) bool { return m.ID == cfg.Model }) {
			fail(422, "agentmodels.unavailable")
			return
		}
	}
	_, e = h.saveAgentModel(r.Context(), id, cfg.Version, cfg)
	if e != nil {
		switch {
		case errors.Is(e, errAgentModelConflict):
			fail(409, "agentmodels.conflict")
		case errors.Is(e, errAgentModelForbidden):
			http.Error(w, http.StatusText(403), 403)
		case errors.Is(e, errAgentModelInvalid):
			fail(422, "agentmodels.invalid")
		default:
			h.serverError(w, r, e)
		}
		return
	}
	httpx.SetFlash(w, "success", "agentmodels.saved")
	redirect(w, r, "/admin/agents/keys?agent_model="+id)
}
