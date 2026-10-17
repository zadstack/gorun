package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

type canvasWidgetOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type canvasMetric = kernel.CanvasMetric
type canvasWidgetRow = kernel.CanvasWidgetRow
type canvasWidgetData = kernel.CanvasWidgetData

type canvasWidgetDefinition struct{ id, href, permission string }

var canvasWidgetDefinitions = []canvasWidgetDefinition{
	{"agents", "/admin/agents/approvals", "mcp.approve"},
	{"jobs", "/admin/system/jobs", PermHealth},
}

func (h *handlers) canvasWidgetAllowed(ctx context.Context, id string) bool {
	for _, def := range h.canvasDefinitions() {
		if def.id != id || !auth.Can(ctx, def.permission) {
			continue
		}
		for _, n := range h.d.Admin.Nav(ctx) {
			if n.Href == def.href {
				return true
			}
		}
	}
	return false
}
func (h *handlers) canvasWidgetOptions(r *http.Request) []canvasWidgetOption {
	options := []canvasWidgetOption{}
	for _, def := range h.canvasDefinitions() {
		if h.canvasWidgetAllowed(r.Context(), def.id) {
			options = append(options, canvasWidgetOption{def.id, h.tr(r, "canvaswidgets."+def.id)})
		}
	}
	return options
}
func (h *handlers) canvasWidget(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, err := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.d.Log.Error("admin: canvas widget owner", "err", err)
		http.Error(w, "Unavailable", 503)
		return
	}
	id := chi.URLParam(r, "widgetID")
	if !h.canvasWidgetAllowed(r.Context(), id) {
		http.NotFound(w, r)
		return
	}
	data, err := h.loadCanvasWidget(r, id)
	if err != nil {
		h.d.Log.Error("admin: canvas widget", "widget", id, "err", err)
		http.Error(w, "Unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.d.Log.Error("admin: canvas widget response", "err", err)
	}
}
func (h *handlers) loadCanvasWidget(r *http.Request, id string) (canvasWidgetData, error) {
	for _, widget := range h.d.Admin.CanvasWidgets() {
		if widget.ID == id {
			return widget.Load(r)
		}
	}
	data := canvasWidgetData{ID: id, Title: h.tr(r, "canvaswidgets."+id), Metrics: []canvasMetric{}, Rows: []canvasWidgetRow{}, UpdatedAt: db.Now()}
	for _, def := range h.canvasDefinitions() {
		if def.id == id {
			data.Href = def.href
		}
	}
	metric := func(key string, value int) {
		data.Metrics = append(data.Metrics, canvasMetric{Label: h.tr(r, "canvaswidgets."+key), Value: strconv.Itoa(value)})
	}
	ctx := r.Context()
	if id == "jobs" {
		stats, err := h.d.Jobs.Stats(ctx)
		if err != nil {
			return data, err
		}
		metric("queued", stats.Queued)
		metric("running", stats.Running)
		metric("failed", stats.Failed)
		metric("dead", stats.Dead)
		return data, nil
	}
	var query string
	var args []any
	switch id {
	case "agents":
		var pending int
		if err := h.d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_approvals WHERE status='pending' AND expires_at>?`, db.Now()).Scan(&pending); err != nil {
			return data, err
		}
		metric("pending", pending)
		query = `SELECT id,tool,status FROM mcp_approvals WHERE status='pending' AND expires_at>? ORDER BY requested_at DESC,id DESC LIMIT 5`
		args = []any{db.Now()}
	}
	rows, err := h.d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var recordID, title, status string
		if err := rows.Scan(&recordID, &title, &status); err != nil {
			return data, err
		}
		href := data.Href

		data.Rows = append(data.Rows, canvasWidgetRow{Title: title, Detail: h.tr(r, "canvaswidgets.status."+status), Href: href})
	}
	return data, rows.Err()
}

func (h *handlers) canvasDefinitions() []canvasWidgetDefinition {
	defs := append([]canvasWidgetDefinition(nil), canvasWidgetDefinitions...)
	for _, widget := range h.d.Admin.CanvasWidgets() {
		defs = append(defs, canvasWidgetDefinition{widget.ID, widget.Href, widget.Permission})
	}
	return defs
}
