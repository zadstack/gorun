package admin

import (
	"net/http"
	"net/url"

	"github.com/zadstack/gorun/internal/auth"
)

// A dock entry is a canvas operation, not a key, model or live client. Keep
// those identities separate and never include source context or credentials.
type agentsDockEntry struct {
	ID         string `json:"id"`
	Board      string `json:"board"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Generation string `json:"generation"`
	Href       string `json:"href"`
	AgentID    string `json:"agent_id,omitempty"`
	AgentName  string `json:"agent_name,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Model      string `json:"model,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Configured bool   `json:"configured"`
	BundleURL  string `json:"bundle_url,omitempty"`
}

func (h *handlers) agentsDock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if auth.UserFromContext(r.Context()) == nil || auth.ActorFromContext(r.Context()).Kind != "user" || !auth.Can(r.Context(), "content.edit") {
		h.canvasOpsError(w, http.StatusForbidden, "forbidden")
		return
	}
	entries := []agentsDockEntry{}
	if !WorkspaceShowsAdvanced(r.Context(), h.d.Settings) {
		h.aiJSON(w, 200, map[string]any{"agents": entries, "available": false})
		return
	}
	boards, err := h.canvasBoards(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	truncated := false
	for _, board := range boards {
		b, err := h.canvasOwned(r.Context(), board.ID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		for _, c := range b.Cards {
			if c.Kind != "agent" && c.Kind != "provider" {
				continue
			}
			if len(entries) == 36 {
				truncated = true
				break
			}
			x := agentsDockEntry{ID: c.ID, Board: b.Title, Title: c.Title, Kind: c.Kind, Href: "/admin/canvases/" + url.PathEscape(b.ID) + "#card-" + url.PathEscape(c.ID)}
			x.Generation = c.GenerationKind
			if x.Generation == "" {
				x.Generation = "text"
			}
			if c.Kind == "agent" {
				x.Mode = "external"
				// Never resolve another user's key, even if its ID was pasted into
				// this user's canvas. Revoked and expired keys are unavailable.
				if k, e := h.canvasAgentKey(r.Context(), c.AgentID); e == nil {
					x.AgentID, x.AgentName = k.ID, k.Name
					if auth.Can(r.Context(), PermAPIKeys) && auth.Can(r.Context(), "settings.manage") {
						x.BundleURL = "/admin/agents/keys/" + k.ID + "/bundle"
					}
					if m, e := h.loadAgentModel(r.Context(), k.ID); e == nil {
						x.Mode = m.Mode
					}
				}
			} else {
				x.Mode = "provider"
			}
			// Preparation uses the actual saved graph, preset and current grants.
			// It is read-only and does not dispatch, consent, or contact a model.
			if p, e := h.canvasPrepare(r.Context(), b, c.ID); e == nil {
				x.Configured = true
				x.Prompt = p.Prompt
				x.Model = p.Provider["model"]
				if p.Candidate.Native != nil {
					x.Model = p.Candidate.Native.Model
				}
				if p.AgentModel != nil {
					x.Model = p.AgentModel.Model
				}
			}
			entries = append(entries, x)
		}
		if truncated {
			break
		}
	}
	h.aiJSON(w, 200, map[string]any{"agents": entries, "available": true, "truncated": truncated})
}
