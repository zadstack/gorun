package admin

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"html/template"
	"net/http"
)

// Only the current owner's still-pending managed-tool runs are projected here.
// Opening this widget neither approves requests nor contacts providers.
func (h *handlers) widgetReviewQueue(ctx context.Context, r *http.Request) (template.HTML, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || !can(r, "content.edit") || !WorkspaceShowsAdvanced(ctx, h.d.Settings) {
		return "", nil
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT DISTINCT b.id,b.title FROM admin_canvas_runs r JOIN admin_canvases b ON b.id=r.board_id AND b.owner_id=r.owner_id WHERE r.owner_id=? AND r.status='waiting_tool' ORDER BY r.created_at DESC LIMIT 6`, u.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type item struct{ ID, Title string }
	items := []item{}
	for rows.Next() {
		var it item
		if err = rows.Scan(&it.ID, &it.Title); err != nil {
			return "", err
		}
		items = append(items, it)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil
	}
	return h.fragment(r, "pages/system/widgets/review_queue.html", items)
}
