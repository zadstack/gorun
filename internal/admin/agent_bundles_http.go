package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

type agentBundleConnection struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"-"`
	Status   string `json:"status"`
	TestedAt string `json:"tested_at,omitempty"`
	Selected bool   `json:"-"`
}
type agentBundlePage struct {
	ID, Name, Error string
	Bundle          agentBundle
	Skills          []agentSkill
	Connections     []agentBundleConnection
	Missing         int
}

func (h *handlers) agentBundleAccess(w http.ResponseWriter, r *http.Request) (*auth.APIKey, bool) {
	if auth.ActorFromContext(r.Context()).Kind != "user" || !auth.Can(r.Context(), PermAPIKeys) || !auth.Can(r.Context(), "settings.manage") || !WorkspaceShowsAdvanced(r.Context(), h.d.Settings) {
		http.Error(w, "Forbidden", 403)
		return nil, false
	}
	k, e := h.canvasAgentKey(r.Context(), urlID(r))
	if e != nil || !agentBundleOwnerCurrent(r.Context(), h.d.DB, k.UserID, k.ID) {
		http.NotFound(w, r)
		return nil, false
	}
	return k, true
}
func (h *handlers) agentBundlePage(r *http.Request, k *auth.APIKey, b agentBundle) (agentBundlePage, error) {
	page := agentBundlePage{ID: k.ID, Name: k.Name, Bundle: b, Skills: append([]agentSkill{}, b.Skills...)}
	for len(page.Skills) < 8 {
		page.Skills = append(page.Skills, agentSkill{})
	}
	refs := map[string]agentMCPReference{}
	for _, ref := range b.MCP {
		refs[ref.ID] = ref
	}
	rows, e := h.listConnections(r.Context())
	if e != nil {
		return page, e
	}
	for _, c := range rows {
		if c.Kind != "mcp" || validateConnection(c) != nil {
			continue
		}
		ref, selected := refs[c.ID]
		delete(refs, c.ID)
		v := agentBundleConnection{ID: c.ID, Version: c.Version, Name: c.Name, Endpoint: c.Endpoint, Status: "configured", Selected: selected}
		if selected && ref.Version != c.Version {
			v.Status = "changed"
		} else if c.TestedAt != "" && (c.TestStatus == "pass" || c.TestStatus == "fail") {
			v.Status = c.TestStatus
			v.TestedAt = c.TestedAt
		}
		page.Connections = append(page.Connections, v)
	}
	page.Missing = len(refs)
	return page, nil
}
func (h *handlers) agentBundleHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k, ok := h.agentBundleAccess(w, r)
	if !ok {
		return
	}
	b, e := readAgentBundle(r.Context(), h.d.DB, k.ID)
	if e != nil || b.Version != "" && b.OwnerID != k.UserID {
		http.Error(w, "Unavailable", 422)
		return
	}
	message := ""
	status := 200
	var proposed *agentBundle
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if r.ParseForm() != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		in := agentBundle{}
		valid := true
		arrays := []string{"skill_name", "skill_instructions", "resource_name", "resource_text"}
		n := len(r.PostForm[arrays[0]])
		if n > 8 {
			valid = false
		}
		for _, name := range arrays {
			if len(r.PostForm[name]) != n {
				valid = false
			}
		}
		if valid {
			for i := 0; i < n; i++ {
				s := agentSkill{Name: strings.TrimSpace(r.PostForm[arrays[0]][i]), Instructions: strings.TrimSpace(r.PostForm[arrays[1]][i]), ResourceName: strings.TrimSpace(r.PostForm[arrays[2]][i]), ResourceText: strings.TrimSpace(r.PostForm[arrays[3]][i])}
				if s != (agentSkill{}) {
					in.Skills = append(in.Skills, s)
				}
			}
		}
		for _, id := range r.PostForm["mcp"] {
			in.MCP = append(in.MCP, agentMCPReference{ID: id, Version: r.PostForm.Get("version_" + id)})
		}
		if valid {
			copy := in
			copy.Version = r.PostForm.Get("version")
			proposed = &copy
		}
		if !valid || r.PostForm.Get("reviewed") != "1" {
			e = errAgentBundle
		} else {
			in, e = h.saveAgentBundle(r.Context(), k.ID, r.PostForm.Get("version"), in)
		}
		if e != nil {
			status = 422
			message = "agentbundles.invalid"
			if errors.Is(e, errAgentModelConflict) {
				status = 409
				message = "agentbundles.conflict"
			}
		} else {
			httpx.SetFlash(w, "success", "agentbundles.saved")
			redirect(w, r, "/admin/agents/keys/"+k.ID+"/bundle")
			return
		}
	}
	if proposed != nil {
		b = *proposed
	}
	page, e := h.agentBundlePage(r, k, b)
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	if !agentBundleOwnerCurrent(r.Context(), h.d.DB, k.UserID, k.ID) {
		http.NotFound(w, r)
		return
	}
	page.Error = message
	h.tpl.RenderWith(w, r, status, "admin", "pages/agents/bundle.html", page)
}
func (h *handlers) agentBundleExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k, ok := h.agentBundleAccess(w, r)
	if !ok {
		return
	}
	b, e := currentAgentBundle(r.Context(), h.d.DB, k.UserID, k.ID)
	if e != nil {
		http.Error(w, "Bundle needs review", 409)
		return
	}
	page, e := h.agentBundlePage(r, k, b)
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	selected := []agentBundleConnection{}
	for _, c := range page.Connections {
		if c.Selected {
			selected = append(selected, c)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="agent-bundle.json"`)
	_ = json.NewEncoder(w).Encode(map[string]any{"schema": "gorun-agent-bundle-v1", "agent_id": k.ID, "version": b.Version, "skills": b.Skills, "mcp": selected, "execution": "MCP references require explicit external-client configuration and credentials. No grants or automatic tool execution are provided."})
}
