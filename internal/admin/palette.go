package admin

import (
	"net/http"

	"github.com/zadstack/gorun/internal/i18n"
)

// The command palette (web/admin/static/command-palette.js) opens with
// Ctrl/Cmd+K on every admin page. It loads this list once per page: the
// admin pages and quick actions the signed-in user may use, each with its
// label in the request locale and the other admin locale so a search in
// either language finds it. Live content and user results come from the
// global search JSON (/admin/x/webmcp/search).

type paletteItem struct {
	Label   string   `json:"label"`
	Alt     []string `json:"alt,omitempty"` // labels in the other admin locales
	Href    string   `json:"href"`
	Icon    string   `json:"icon,omitempty"`
	Section string   `json:"section,omitempty"`
}

type paletteData struct {
	Pages   []paletteItem `json:"pages"`
	Actions []paletteItem `json:"actions"`
}

// paletteNavActions are quick actions derived from navigation items: each
// is offered only when the user's permission-filtered navigation holds the
// item, so the palette never grants more than the sidebar.
var paletteNavActions = []struct{ NavID, LabelKey, Href, Icon, Perm string }{
	{"media", "palette.action.upload_media", "/admin/media#media-upload", "upload", "media.upload"},
	{"settings", "palette.action.open_settings", "/admin/settings/general", "settings", PermSettings},
}

func (h *handlers) paletteRoutes() {
	h.d.Router.Admin.Get("/x/palette", h.palette)
}

// otherLabels translates key into every supported admin locale except the
// request's, skipping untranslated keys and duplicates of label.
func (h *handlers) otherLabels(r *http.Request, label string, key string) []string {
	if h.d.I18n == nil {
		return nil
	}
	current := i18n.Locale(r.Context())
	var out []string
	for _, loc := range []string{"en", "ar"} {
		if loc == current {
			continue
		}
		if v := h.d.I18n.T(loc, key); v != key && v != label {
			out = append(out, v)
		}
	}
	return out
}

func (h *handlers) palette(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := paletteData{Pages: []paletteItem{}, Actions: []paletteItem{}}
	nav := h.d.Admin.Nav(ctx)
	inNav := map[string]bool{}
	for _, n := range nav {
		inNav[n.ID] = true
		if n.Href == "" {
			continue
		}
		label := h.tr(r, n.LabelKey)
		item := paletteItem{Label: label, Alt: h.otherLabels(r, label, n.LabelKey), Href: n.Href, Icon: n.Icon}
		if n.Group != "" {
			if key := "core.nav.group." + n.Group; h.tr(r, key) != key && h.tr(r, key) != label {
				item.Section = h.tr(r, key)
			}
		}
		out.Pages = append(out.Pages, item)
	}
	for _, p := range extraPages {
		if !can(r, p.Perm) || (p.Href == "/admin/settings/ai" && !WorkspaceShowsAdvanced(ctx, h.d.Settings)) {
			continue
		}
		label := h.tr(r, p.LabelKey)
		out.Pages = append(out.Pages, paletteItem{Label: label, Alt: h.otherLabels(r, label, p.LabelKey), Href: p.Href, Icon: p.Icon})
	}
	actions, err := h.quickActions(ctx, r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	locale := i18n.Locale(ctx)
	for _, a := range actions {
		item := paletteItem{Href: a.Href, Icon: a.Icon}
		if a.LabelKey != "" {
			item.Label = h.tr(r, a.LabelKey)
			item.Alt = h.otherLabels(r, item.Label, a.LabelKey)
		} else {
			item.Label = h.tr(r, "admin.dashboard.new_item", a.Label)
			for loc, name := range a.Names {
				if loc != locale && name != "" && h.d.I18n != nil {
					item.Alt = append(item.Alt, h.d.I18n.T(loc, "admin.dashboard.new_item", name))
				}
			}
		}
		out.Actions = append(out.Actions, item)
	}
	for _, a := range paletteNavActions {
		if !inNav[a.NavID] || !can(r, a.Perm) {
			continue
		}
		label := h.tr(r, a.LabelKey)
		out.Actions = append(out.Actions, paletteItem{Label: label, Alt: h.otherLabels(r, label, a.LabelKey), Href: a.Href, Icon: a.Icon})
	}
	h.aiJSON(w, http.StatusOK, out)
}
