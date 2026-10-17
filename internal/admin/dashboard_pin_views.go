package admin

import (
	"context"
	"html/template"
	"net/http"
	"strconv"

	"github.com/zadstack/gorun/internal/auth"
)

type dashboardPinView struct {
	ID, Title, Href, Icon, Hint string
	Rows                        []canvasWidgetRow
	Metrics                     []canvasMetric
	Projection, Failed          bool
}

func (h *handlers) widgetDashboardPins(ctx context.Context, r *http.Request) (template.HTML, error) {
	pins, err := h.dashboardPinned(ctx)
	if err != nil {
		return "", err
	}
	views := []dashboardPinView{}
	for _, pin := range pins {
		view := dashboardPinView{ID: pin.ID, Title: h.tr(r, pin.LabelKey), Href: pin.Href, Icon: pin.Icon, Rows: []canvasWidgetRow{}, Metrics: []canvasMetric{}}
		if pin.ID == "content.metricool" {
			h.metricoolPin(r, &view)
		} else {
			widgetID := map[string]string{"agents.approvals": "agents", "system.jobs": "jobs"}[pin.ID]
			for _, widget := range h.d.Admin.CanvasWidgets() {
				if widget.NavID == pin.ID {
					widgetID = widget.ID
					break
				}
			}
			if widgetID != "" && h.canvasWidgetAllowed(ctx, widgetID) {
				view.Projection = true
				data, err := h.loadCanvasWidget(r, widgetID)
				if err != nil {
					h.d.Log.Error("admin: pinned dashboard widget", "err", err)
					view.Failed = true
				} else {
					view.Rows, view.Metrics = data.Rows, data.Metrics
				}
			}
		}
		views = append(views, view)
	}
	return h.fragment(r, "pages/system/widgets/pins.html", struct{ Pinned []dashboardPinView }{views})
}
func (h *handlers) metricoolPin(r *http.Request, view *dashboardPinView) {
	view.Projection = true
	view.Hint = h.tr(r, "pinmetricool.hint")
	u := auth.UserFromContext(r.Context())
	if u == nil || !auth.Can(r.Context(), "content.publish") {
		view.Failed = true
		return
	}
	var confirmed, uncertain int
	err := h.d.DB.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(json_extract(value_json,'$.status')='confirmed'),0),COALESCE(SUM(json_extract(value_json,'$.status')='uncertain'),0) FROM settings WHERE key LIKE 'metricool.draft.%' AND json_extract(value_json,'$.actor')=?`, u.ID).Scan(&confirmed, &uncertain)
	if err != nil {
		view.Failed = true
		h.d.Log.Error("admin: pinned Metricool summary", "err", err)
		return
	}
	view.Metrics = []canvasMetric{{Label: h.tr(r, "pinmetricool.confirmed"), Value: strconv.Itoa(confirmed)}, {Label: h.tr(r, "pinmetricool.uncertain"), Value: strconv.Itoa(uncertain)}}
	rows, err := h.d.DB.QueryContext(r.Context(), `SELECT json_extract(value_json,'$.status'),json_extract(value_json,'$.created_at') FROM settings WHERE key LIKE 'metricool.draft.%' AND json_extract(value_json,'$.actor')=? ORDER BY json_extract(value_json,'$.created_at') DESC,key DESC LIMIT 5`, u.ID)
	if err != nil {
		view.Failed = true
		h.d.Log.Error("admin: pinned Metricool rows", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status, created string
		if err := rows.Scan(&status, &created); err != nil {
			view.Failed = true
			h.d.Log.Error("admin: pinned Metricool row", "err", err)
			return
		}
		key := "pinmetricool.uncertain"
		if status == "confirmed" {
			key = "pinmetricool.confirmed"
		}
		view.Rows = append(view.Rows, canvasWidgetRow{Title: h.tr(r, key), Detail: created, Href: "/admin/metricool"})
	}
	if err := rows.Err(); err != nil {
		view.Failed = true
		h.d.Log.Error("admin: pinned Metricool result", "err", err)
	}
}
