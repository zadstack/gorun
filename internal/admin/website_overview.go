package admin

import (
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

type websiteOverviewGroup struct {
	Key   string
	Links []kernel.NavItem
}

func (h *handlers) websiteOverview(w http.ResponseWriter, r *http.Request) {
	groups := []websiteOverviewGroup{{Key: "pages"}, {Key: "design"}, {Key: "search"}}
	for _, item := range h.d.Admin.Nav(r.Context()) {
		index := -1
		switch {
		case item.ID == "menus" || item.ID == "content.type.page" || item.ID == "appearance.overview":
			index = 0
		case strings.HasPrefix(item.ID, "seo") || strings.HasPrefix(item.ID, "redirect") || strings.Contains(item.Href, "/settings/seo"):
			index = 2
		case item.Group == "appearance":
			index = 1
		}
		if strings.Contains(item.Href, "/readiness") {
			index = 2
		}
		if index >= 0 {
			groups[index].Links = append(groups[index].Links, item)
		}
	}
	visible := []websiteOverviewGroup{}
	for _, g := range groups {
		if len(g.Links) > 0 {
			visible = append(visible, g)
		}
	}
	if len(visible) == 0 {
		h.tpl.RenderError(w, r, http.StatusForbidden)
		return
	}
	h.tpl.Render(w, r, "pages/appearance/website.html", struct{ Groups []websiteOverviewGroup }{visible})
}
