package admin

import (
	"context"
	"html/template"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// Site workspace mode: progressive disclosure for operators who want a
// plain CMS versus Agents + Commerce. Empty means unset (full nav until
// chosen; first-run surfaces a chooser).
const (
	SettingWorkspace       = "site.workspace"
	WorkspaceSimple        = "simple"
	WorkspaceFull          = "full"
	maxWorkspaceTitleRunes = 200
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
	Mode           string
	Title, Tagline string
	NeedIdentity   bool
	Fields         map[string]string
	Saved          bool
	First          bool
}

func (h *handlers) workspacePageData(ctx context.Context, mode string) workspacePage {
	title := strings.TrimSpace(h.d.Settings.GetString(ctx, "site.title", ""))
	return workspacePage{
		Mode:         mode,
		Title:        title,
		Tagline:      h.d.Settings.GetString(ctx, "site.tagline", ""),
		NeedIdentity: title == "",
		First:        mode == "",
	}
}

func (h *handlers) workspaceGet(w http.ResponseWriter, r *http.Request) {
	mode := WorkspaceMode(r.Context(), h.d.Settings)
	page := h.workspacePageData(r.Context(), mode)
	page.Saved = r.URL.Query().Get("saved") == "1"
	h.tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/settings/workspace.html", page)
}

func (h *handlers) workspaceSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode    string `form:"mode"`
		Title   string `form:"title"`
		Tagline string `form:"tagline"`
	}
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	page := h.workspacePageData(ctx, WorkspaceMode(ctx, h.d.Settings))
	page.Fields = map[string]string{}
	mode := strings.TrimSpace(in.Mode)
	if mode != WorkspaceSimple && mode != WorkspaceFull {
		page.Fields["mode"] = "admin.workspace.error_mode"
	}
	title := strings.TrimSpace(in.Title)
	tagline := strings.TrimSpace(in.Tagline)
	if page.NeedIdentity {
		page.Title, page.Tagline = title, tagline
		if title == "" || utf8.RuneCountInString(title) > maxWorkspaceTitleRunes {
			page.Fields["title"] = "admin.workspace.error_title"
		}
		if utf8.RuneCountInString(tagline) > maxWorkspaceTitleRunes {
			page.Fields["tagline"] = "admin.workspace.error_tagline"
		}
	}
	if len(page.Fields) > 0 {
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/settings/workspace.html", page)
		return
	}
	if page.NeedIdentity {
		if err := h.d.Settings.Set(ctx, "site.title", title); err != nil {
			h.serverError(w, r, err)
			return
		}
		if err := h.d.Settings.Set(ctx, "site.tagline", tagline); err != nil {
			h.serverError(w, r, err)
			return
		}
		if strings.TrimSpace(h.d.Settings.GetString(ctx, "site.locale", "")) == "" {
			_ = h.d.Settings.Set(ctx, "site.locale", i18n.Locale(ctx))
		}
		h.d.Audit.Record(ctx, "settings.identity", "settings", "site.title", map[string]string{"source": "workspace"})
	}
	if err := h.d.Settings.Set(ctx, SettingWorkspace, mode); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "settings.workspace", "settings", SettingWorkspace, map[string]string{"mode": mode})
	httpx.SetFlash(w, "success", "admin.workspace.saved")
	http.Redirect(w, r, "/admin/workspace?saved=1", http.StatusSeeOther)
}

func (h *handlers) widgetWorkspaceChoice(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermSettings) || WorkspaceMode(ctx, h.d.Settings) != "" {
		return "", nil
	}
	return h.fragment(r, "pages/system/widgets/workspace_choice.html", nil)
}
