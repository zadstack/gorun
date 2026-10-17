package admin

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
)

type errorInventoryRow struct {
	Source, Category, Code, Status, FirstAt, LastAt, Href string
	HTTPStatus                                            int
	Count                                                 int64
}
type errorInventoryFilter struct {
	Source, Category, Status, Since, Until string
	HTTPStatus                             int
}
type errorInventoryPage struct {
	Rows                          []errorInventoryRow
	Filter                        errorInventoryFilter
	Categories, Sources, Statuses []string
	Total                         int64
	Dropped, StorageFailures      uint64
	CollectorAvailable            bool
	CollectorReadAt, LastWriteAt  string
}

func inventoryOwner(ctx context.Context) bool {
	u := auth.UserFromContext(ctx)
	return u != nil && u.Role == auth.RoleAdmin && auth.Can(ctx, PermHealth) && auth.ActorFromContext(ctx).Kind != "api_key"
}
func (h *handlers) errorInventoryNav() {
	h.d.Admin.AddNavProvider(func(ctx context.Context) []kernel.NavItem {
		if !inventoryOwner(ctx) {
			return nil
		}
		return []kernel.NavItem{{ID: "system.errors", LabelKey: "errorinventory.title", Icon: "triangle-alert", Href: "/admin/system/errors", Permission: PermHealth, Group: "system", Position: 905}}
	})
}
func (h *handlers) errorInventoryRoutes() { h.d.Router.Admin.Get("/system/errors", h.errorInventory) }
func (h *handlers) errorInventory(w http.ResponseWriter, r *http.Request) {
	if !inventoryOwner(r.Context()) {
		h.tpl.RenderError(w, r, 403)
		return
	}
	q := r.URL.Query()
	f := errorInventoryFilter{Source: q.Get("source"), Category: q.Get("category"), Status: q.Get("status"), Since: q.Get("since"), Until: q.Get("until")}
	if !slices.Contains([]string{"", "runtime", "jobs", "audit"}, f.Source) || !slices.Contains(append([]string{""}, diagnostics.Categories()...), f.Category) || !slices.Contains([]string{"", "recorded", "retrying", "dead", "failed"}, f.Status) {
		h.tpl.RenderError(w, r, 400)
		return
	}
	if raw := q.Get("http_status"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 500 || n > 599 {
			h.tpl.RenderError(w, r, 400)
			return
		}
		f.HTTPStatus = n
	}
	for _, v := range []string{f.Since, f.Until} {
		if v != "" {
			if _, e := time.Parse(time.DateOnly, v); e != nil {
				h.tpl.RenderError(w, r, 400)
				return
			}
		}
	}
	if f.Since != "" && f.Until != "" && f.Since > f.Until {
		h.tpl.RenderError(w, r, 400)
		return
	}
	rows, e := h.errorInventoryRows(r.Context(), f)
	if e != nil {
		h.serverError(w, r, e)
		return
	}
	for i := range rows {
		if rows[i].Source == "runtime" {
			rows[i].Href = "/admin/system/health"
		}
	}
	data := errorInventoryPage{Rows: rows, Filter: f, Categories: diagnostics.Categories(), Sources: []string{"runtime", "jobs", "audit"}, Statuses: []string{"recorded", "retrying", "dead", "failed"}}
	for _, v := range rows {
		data.Total += v.Count
	}
	if c, ok := kernel.Get[*diagnostics.Collector](h.d, diagnostics.ServiceName); ok {
		data.CollectorAvailable = true
		if last := c.LastSuccessfulWrite(); !last.IsZero() {
			data.LastWriteAt = last.Format(time.RFC3339)
		}
		data.Dropped = c.Dropped()
		data.StorageFailures = c.StorageFailures()
		data.CollectorReadAt = time.Now().UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.Render(w, r, "pages/system/errors.html", data)
}
