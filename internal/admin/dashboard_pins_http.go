package admin

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *handlers) dashboardPinRoutes() {
	r := h.d.Router.Admin
	r.Post("/dashboard/pins", h.dashboardPinAdd)
	r.Post("/dashboard/pins/order", h.dashboardPinOrder)
	r.Post("/dashboard/pins/{navID}/remove", h.dashboardPinRemove)
}
func (h *handlers) dashboardPinResult(w http.ResponseWriter, r *http.Request, e error, status int) {
	if e != nil {
		switch {
		case errors.Is(e, errDashboardPinInvalid):
			h.aiJSON(w, status, map[string]string{"error": "invalid"})
		case errors.Is(e, errDashboardPinLimit):
			h.aiJSON(w, 422, map[string]string{"error": "limit"})
		default:
			h.d.Log.Error("admin: dashboard pin", "err", e)
			h.aiJSON(w, 500, map[string]string{"error": "unavailable"})
		}
		return
	}
	h.aiJSON(w, 200, map[string]bool{"ok": true})
}
func (h *handlers) dashboardPinAdd(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil || len(r.PostForm["nav_id"]) != 1 {
		h.dashboardPinResult(w, r, errDashboardPinInvalid, 422)
		return
	}
	id := r.PostForm.Get("nav_id")
	e := h.setDashboardPin(r.Context(), id, true)
	if e == nil {
		h.d.Audit.Record(r.Context(), "dashboard.pin", "navigation", id, nil)
	}
	h.dashboardPinResult(w, r, e, 403)
}
func (h *handlers) dashboardPinRemove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "navID")
	e := h.setDashboardPin(r.Context(), id, false)
	if e == nil {
		h.d.Audit.Record(r.Context(), "dashboard.unpin", "navigation", id, nil)
	}
	h.dashboardPinResult(w, r, e, 403)
}
func (h *handlers) dashboardPinOrder(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.dashboardPinResult(w, r, errDashboardPinInvalid, 422)
		return
	}
	e := h.orderDashboardPins(r.Context(), r.PostForm["nav_id"])
	if e == nil {
		h.d.Audit.Record(r.Context(), "dashboard.reorder", "navigation", "", nil)
	}
	h.dashboardPinResult(w, r, e, 422)
}
