package admin

import (
	"context"
	"html/template"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

type workspaceRow struct{ Title, Href, StatusKey, UpdatedAt, Locale string }
type dashboardWorkspace struct {
	Rows                []workspaceRow
	Failed, ShowContent bool
}

func (h *handlers) widgetWorkspace(ctx context.Context, r *http.Request) (template.HTML, error) {
	data := dashboardWorkspace{}
	cs, ok := h.contentService()
	if !ok || !can(r, "content.view") {
		return "", nil
	}
	if ok {
		data.ShowContent = true
		query := content.Query{PerPage: 6, PublicOnly: true, Order: []content.Order{{By: "updated_at", Dir: "desc"}}}
		if u := auth.UserFromContext(ctx); u != nil && (u.Role == "author" || u.Role == "contributor") {
			query.AuthorID = u.ID
		}
		result, err := cs.Query(ctx, query)
		if err != nil {
			h.d.Log.Error("admin: dashboard content", "err", err)
			data.Failed = true
		} else {
			for _, item := range result.Items {
				data.Rows = append(data.Rows, workspaceRow{Title: item.Title, Href: "/admin/content/" + item.Type + "/" + item.ID, StatusKey: "content.status." + string(item.Status), UpdatedAt: item.UpdatedAt, Locale: item.Locale})
			}
		}
	}
	return h.fragment(r, "pages/system/widgets/workspace.html", data)
}
