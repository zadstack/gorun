package admin

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
)

// A read-only, owner-scoped projection for any admin page. The optional page
// path only controls active styling, never routing, permission checks or fetches.
func (h *handlers) sidebarQuickAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if auth.UserFromContext(r.Context()) == nil || auth.ActorFromContext(r.Context()).Kind != "user" {
		h.aiJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	page := r.URL.Query().Get("page")
	if page != "" {
		u, e := url.Parse(page)
		if e != nil || len(page) > 2048 || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "/admin" && !strings.HasPrefix(u.Path, "/admin/")) {
			h.aiJSON(w, 422, map[string]string{"error": "invalid"})
			return
		}
		copy := r.Clone(r.Context())
		cloned := *r.URL
		cloned.Path = u.Path
		copy.URL = &cloned
		r = copy
	}
	pins, e := h.dashboardPinned(r.Context())
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	ids := []string{}
	for _, pin := range pins {
		ids = append(ids, pin.ID)
	}
	quick, e := h.fragment(r, "pages/system/widgets/quick_access.html", nil)
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	widget := ""
	if r.URL.Query().Get("widget") == "1" {
		html, e := h.widgetDashboardPins(r.Context(), r)
		if e != nil {
			h.serverError(w, r, e)
			return
		}
		widget = string(html)
	}
	h.aiJSON(w, 200, map[string]any{"ids": ids, "quick_html": string(quick), "widget_html": widget})
}
