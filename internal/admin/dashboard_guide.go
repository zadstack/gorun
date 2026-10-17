package admin

import (
	"context"
	"html/template"
	"net/http"
	"slices"

	"github.com/zadstack/gorun/internal/kernel"
)

type workspaceGuideArea struct {
	Key, LabelKey, DescriptionKey string
	Nav                           kernel.NavItem
}

func (h *handlers) widgetWorkspaceGuide(ctx context.Context, r *http.Request) (template.HTML, error) {
	nav := h.d.Admin.Nav(ctx)
	areas := []workspaceGuideArea{}
	for _, def := range []struct {
		key               string
		groups, preferred []string
	}{
		{"content", []string{"content", "structure"}, []string{"content.type.post", "content.type.page"}},
		{"inbox", []string{"inbox"}, []string{"system.form_submissions", "site.private_intake"}},
		{"appearance", []string{"appearance"}, []string{"appearance.overview", "appearance.themes"}},
		{"agents", []string{"agents"}, []string{"agents.approvals", "agents.keys"}},
		{"customers", []string{"commerce"}, []string{"commerce.orders"}},
		{"tools", []string{"settings", "integrations", "system"}, []string{"settings", "settings.connections", "system.health"}},
	} {
		var chosen *kernel.NavItem
		for _, id := range def.preferred {
			for _, n := range nav {
				if n.ID == id && slices.Contains(def.groups, n.Group) {
					copy := n
					chosen = &copy
					break
				}
			}
			if chosen != nil {
				break
			}
		}
		if chosen == nil {
			for _, n := range nav {
				if slices.Contains(def.groups, n.Group) {
					copy := n
					chosen = &copy
					break
				}
			}
		}
		if chosen != nil {
			areas = append(areas, workspaceGuideArea{def.key, "workspaceguide." + def.key, "workspaceguide." + def.key + "_help", *chosen})
		}
	}
	return h.fragment(r, "pages/system/widgets/guide.html", struct{ Areas []workspaceGuideArea }{areas})
}
