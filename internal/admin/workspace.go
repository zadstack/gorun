package admin

import (
	"context"
	"html/template"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// Site workspace mode: progressive disclosure for operators who want a
// plain CMS versus Agents + Commerce. Empty means unset (full nav until
// chosen; first-run surfaces a chooser).
const (
	SettingWorkspace = "site.workspace"
	WorkspaceSimple  = "simple"
	WorkspaceFull    = "full"
)

// Groups and nav IDs hidden when site.workspace is "simple".
var (
	simpleHiddenGroups = map[string]bool{
		"agents":   true,
		"commerce": true,
	}
	simpleHiddenIDs = map[string]bool{
		"canvases":             true,
		"workagents.workspace": true,
		"agents.audit":         true,
		"settings.connections": true,
	}
)

// WorkspaceMode returns simple, full, or "" when unset / unknown.
func WorkspaceMode(ctx context.Context, s *settings.Store) string {
	if s == nil {
		return ""
	}
	switch v := strings.TrimSpace(s.GetString(ctx, SettingWorkspace, "")); v {
	case WorkspaceSimple, WorkspaceFull:
		return v
	default:
		return ""
	}
}

// WorkspaceShowsAdvanced is true when Agents / Commerce / AI surfaces
// should appear (full mode, or unset so existing installs keep working).
func WorkspaceShowsAdvanced(ctx context.Context, s *settings.Store) bool {
	return WorkspaceMode(ctx, s) != WorkspaceSimple
}

// FilterNavForWorkspace drops Agents/Commerce surfaces in simple mode.
func FilterNavForWorkspace(mode string, items []kernel.NavItem) []kernel.NavItem {
	if mode != WorkspaceSimple {
		return items
	}
	out := make([]kernel.NavItem, 0, len(items))
	for _, n := range items {
		if simpleHiddenGroups[n.Group] || simpleHiddenIDs[n.ID] {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (h *handlers) registerWorkspaceFilter() {
	h.d.Admin.SetNavFilter(func(ctx context.Context, items []kernel.NavItem) []kernel.NavItem {
		return FilterNavForWorkspace(WorkspaceMode(ctx, h.d.Settings), items)
	})
}

type workspacePage struct {
	Mode   string
	Fields map[string]string
	Saved  bool
	First  bool
}

func (h *handlers) workspaceGet(w http.ResponseWriter, r *http.Request) {
	mode := WorkspaceMode(r.Context(), h.d.Settings)
	h.tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/settings/workspace.html", workspacePage{
		Mode:  mode,
		First: mode == "",
		Saved: r.URL.Query().Get("saved") == "1",
	})
}

func (h *handlers) workspaceSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `form:"mode"`
	}
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(in.Mode)
	if mode != WorkspaceSimple && mode != WorkspaceFull {
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/settings/workspace.html", workspacePage{
			Mode:   WorkspaceMode(r.Context(), h.d.Settings),
			First:  WorkspaceMode(r.Context(), h.d.Settings) == "",
			Fields: map[string]string{"mode": "admin.workspace.error_mode"},
		})
		return
	}
	if err := h.d.Settings.Set(r.Context(), SettingWorkspace, mode); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(r.Context(), "settings.workspace", "settings", SettingWorkspace, map[string]string{"mode": mode})
	httpx.SetFlash(w, "success", "admin.workspace.saved")
	http.Redirect(w, r, "/admin/workspace?saved=1", http.StatusSeeOther)
}

func (h *handlers) widgetWorkspaceChoice(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermSettings) || WorkspaceMode(ctx, h.d.Settings) != "" {
		return "", nil
	}
	return h.fragment(r, "pages/system/widgets/workspace_choice.html", nil)
}
