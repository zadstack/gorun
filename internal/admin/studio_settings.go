package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
)

type studioDiscovery struct {
	Version string       `json:"version"`
	Tools   []studioTool `json:"tools"`
}

func (h *handlers) studioSettings(w http.ResponseWriter, r *http.Request) {
	c, e := h.studioConfig(r.Context())
	if e != nil {
		h.studioError(w, 500)
		return
	}
	raw, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		h.studioError(w, 500)
		return
	}
	connections, e := h.listConnections(r.Context())
	if e != nil {
		h.studioError(w, 500)
		return
	}
	rows := []connectionView{}
	for _, c := range connections {
		if c.Kind == "mcp" {
			rows = append(rows, viewConnection(c))
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, 200, "admin", "pages/settings/studio.html", struct {
		Config      string
		Connections []connectionView
	}{string(raw), rows})
}
func (h *handlers) studioDiscover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if studioRead(w, r, &in) != nil {
		h.studioError(w, 400)
		return
	}
	c, e := h.loadConnection(r.Context(), in.ID)
	if e != nil || c.Kind != "mcp" {
		h.studioError(w, 400)
		return
	}
	secret, e := h.connectionSecrets(c)
	if e != nil {
		h.studioError(w, 400)
		return
	}
	if !h.aiLimit.allow("studio-discovery:" + auth.UserFromContext(r.Context()).ID) {
		h.studioError(w, 429)
		return
	}
	ctx, cancel := studioTimeout(r)
	defer cancel()
	session, e := openStudioMCP(ctx, studioSafeClient(), c, secret)
	if e != nil {
		h.studioError(w, 502)
		return
	}
	listed, e := session.tools(ctx)
	if e != nil {
		h.studioError(w, 502)
		return
	}
	if h.d.Settings.Set(ctx, "ai.studio.discovery."+c.ID, studioDiscovery{c.Version, listed}) != nil {
		h.studioError(w, 500)
		return
	}
	h.aiJSON(w, 200, map[string]any{"tools": listed})
}
func (h *handlers) studioSettingsSave(w http.ResponseWriter, r *http.Request) {
	var cfg studioConfig
	if studioRead(w, r, &cfg) != nil || validateStudioConfig(cfg) != nil {
		h.studioError(w, 400)
		return
	}
	for i, b := range cfg.Bindings {
		c, e := h.loadConnection(r.Context(), b.ConnectionID)
		if e != nil || c.Kind != "mcp" {
			h.studioError(w, 400)
			return
		}
		var discovery studioDiscovery
		if h.d.Settings.Get(r.Context(), "ai.studio.discovery."+c.ID, &discovery) != nil || discovery.Version != c.Version {
			h.studioError(w, 409)
			return
		}
		cfg.Bindings[i].ConnectionVersion = c.Version
		cfg.Bindings[i].Endpoint = c.Endpoint
		found, status := false, b.StatusTool == ""
		for _, t := range discovery.Tools {
			if t.Name == b.Tool {
				found = true
			}
			if t.Name == b.StatusTool {
				status = true
			}
		}
		if !found || !status {
			h.studioError(w, 400)
			return
		}
		// Connection secrets belong only in encrypted Connections settings, never argument templates.
		lower := strings.ToLower(string(b.Arguments) + string(b.StatusArguments))
		for _, k := range []string{`"authorization"`, `"api_key"`, `"access_token"`, `"password"`, `"secret"`} {
			if strings.Contains(lower, k) {
				h.studioError(w, 400)
				return
			}
		}
	}
	if h.d.Settings.Set(r.Context(), "ai.studio", cfg) != nil {
		h.studioError(w, 500)
		return
	}
	h.d.Audit.Record(r.Context(), "studio.configure", "settings", "ai.studio", map[string]any{"bindings": len(cfg.Bindings), "presets": len(cfg.Presets)})
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}
