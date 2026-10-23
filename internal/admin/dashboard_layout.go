package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"math"
	"net/http"
	"slices"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

var errDashboardLayoutConflict = errors.New("dashboard layout changed")

type dashboardLayout struct {
	Version int64    `json:"version"`
	Order   []string `json:"order"`
}
type dashboardRendered struct {
	ID   string
	HTML template.HTML
}

func (h *handlers) dashboardLayout(ctx context.Context) (dashboardLayout, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return dashboardLayout{}, errors.New("dashboard layout: authentication required")
	}
	var state dashboardLayout
	var raw string
	err := h.d.DB.QueryRowContext(ctx, `SELECT version,order_json FROM admin_dashboard_layout WHERE user_id=?`, user.ID).Scan(&state.Version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal([]byte(raw), &state.Order)
	return state, err
}

func (h *handlers) dashboardRendered(r *http.Request, strict bool) ([]dashboardRendered, error) {
	var out []dashboardRendered
	for _, wd := range h.d.Admin.Widgets() {
		html, err := wd.Render(r.Context(), r)
		if err != nil {
			if strict {
				return nil, err
			}
			h.d.Log.Error("admin: widget", "widget", wd.ID, "err", err)
			continue
		}
		if html != "" {
			out = append(out, dashboardRendered{wd.ID, html})
		}
	}
	// Match the existing visual defaults before applying personal order.
	rank := func(id string) int {
		switch id {
		case "review_queue":
			return -2
		case "setup_status":
			return -1
		case "workspace_choice":
			return 6
		case "quick_actions":
			return 1
		case "workspace_guide":
			return 7
		case "dashboard_pins":
			return 2
		case "workspace":
			return 0
		case "canvas_gallery":
			return 5
		case "recent_audit", "jobs_summary", "system_summary":
			return 10
		default:
			return 4
		}
	}
	slices.SortStableFunc(out, func(a, b dashboardRendered) int { return rank(a.ID) - rank(b.ID) })
	return out, nil
}

func orderedDashboard(widgets []dashboardRendered, order []string) []dashboardRendered {
	byID := map[string]dashboardRendered{}
	for _, w := range widgets {
		byID[w.ID] = w
	}
	out := make([]dashboardRendered, 0, len(widgets))
	for _, id := range order {
		if w, ok := byID[id]; ok {
			out = append(out, w)
			delete(byID, id)
		}
	}
	for _, w := range widgets {
		if _, ok := byID[w.ID]; ok {
			out = append(out, w)
			delete(byID, w.ID)
		}
	}
	return out
}

func (h *handlers) dashboardLayoutRoutes() {
	h.d.Router.Admin.Post("/dashboard/layout", func(w http.ResponseWriter, r *http.Request) { h.dashboardLayoutSave(w, r, false) })
	h.d.Router.Admin.Post("/dashboard/layout/reset", func(w http.ResponseWriter, r *http.Request) { h.dashboardLayoutSave(w, r, true) })
}

func (h *handlers) dashboardLayoutSave(w http.ResponseWriter, r *http.Request, reset bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		h.aiJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var in dashboardLayout
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.Decode(new(any)) != io.EOF || in.Version < 0 || in.Version == math.MaxInt64 || len(in.Order) > 100 {
		h.aiJSON(w, 422, map[string]string{"error": "invalid"})
		return
	}
	widgets, err := h.dashboardRendered(r, true)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	allowed := map[string]bool{}
	for _, wd := range widgets {
		allowed[wd.ID] = true
	}
	if reset {
		if len(in.Order) > 0 {
			h.aiJSON(w, 422, map[string]string{"error": "invalid"})
			return
		}
	} else {
		seen := map[string]bool{}
		for _, id := range in.Order {
			if !allowed[id] || seen[id] || len(id) > 100 {
				h.aiJSON(w, 422, map[string]string{"error": "invalid"})
				return
			}
			seen[id] = true
		}
		if len(seen) != len(allowed) {
			h.aiJSON(w, 422, map[string]string{"error": "invalid"})
			return
		}
	}
	err = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		var version int64
		var raw string
		e := tx.QueryRowContext(r.Context(), `SELECT version,order_json FROM admin_dashboard_layout WHERE user_id=?`, user.ID).Scan(&version, &raw)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if version != in.Version {
			return errDashboardLayoutConflict
		}
		order := slices.Clone(in.Order)
		if !reset && raw != "" {
			var old []string
			if e = json.Unmarshal([]byte(raw), &old); e != nil {
				return e
			}
			for _, id := range old {
				if !allowed[id] && !slices.Contains(order, id) {
					order = append(order, id)
				}
			}
		}
		if len(order) > 200 {
			return errDashboardLayoutConflict
		}
		encoded, e := json.Marshal(order)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(r.Context(), `INSERT INTO admin_dashboard_layout(user_id,version,order_json,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET version=excluded.version,order_json=excluded.order_json,updated_at=excluded.updated_at`, user.ID, version+1, string(encoded), db.Now())
		return e
	})
	if errors.Is(err, errDashboardLayoutConflict) {
		h.aiJSON(w, 409, map[string]string{"error": "conflict"})
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(r.Context(), "dashboard.layout", "user", user.ID, map[string]any{"reset": reset})
	h.aiJSON(w, 200, map[string]any{"ok": true, "version": in.Version + 1})
}
