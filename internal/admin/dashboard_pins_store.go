package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const maxDashboardPins = 50

var errDashboardPinInvalid = errors.New("invalid dashboard pin")
var errDashboardPinLimit = errors.New("dashboard pin limit")

type dashboardPin struct {
	ID, LabelKey, Href, Icon string
	Position                 int
}

func (h *handlers) dashboardPinNav(ctx context.Context) map[string]kernel.NavItem {
	out := map[string]kernel.NavItem{}
	for _, n := range h.d.Admin.Nav(ctx) {
		if n.ID != "dashboard" && n.ID != "" && len(n.ID) <= 256 {
			out[n.ID] = n
		}
	}
	return out
}
func (h *handlers) dashboardPinned(ctx context.Context) ([]dashboardPin, error) {
	out := []dashboardPin{}
	u := auth.UserFromContext(ctx)
	if u == nil {
		return out, sql.ErrNoRows
	}
	allowed := h.dashboardPinNav(ctx)
	rows, e := h.d.DB.QueryContext(ctx, `SELECT nav_id,position FROM admin_dashboard_pins WHERE user_id=? ORDER BY position,nav_id`, u.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var position int
		if e = rows.Scan(&id, &position); e != nil {
			return nil, e
		}
		if n, ok := allowed[id]; ok {
			out = append(out, dashboardPin{id, n.LabelKey, n.Href, n.Icon, position})
		}
	}
	return out, rows.Err()
}
func restorePinVisibility(ctx context.Context, tx *sql.Tx, userID, id string) error {
	var raw string
	e := tx.QueryRowContext(ctx, `SELECT hidden_json FROM user_navigation_preferences WHERE user_id=?`, userID).Scan(&raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	ids := []string{}
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &ids); e != nil {
			return e
		}
	}
	out := make([]string, 0, len(ids)+1)
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	encoded, e := json.Marshal(out)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO user_navigation_preferences(user_id,hidden_json,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET hidden_json=excluded.hidden_json,updated_at=excluded.updated_at`, userID, string(encoded), db.Now())
	return e
}
func (h *handlers) setDashboardPin(ctx context.Context, id string, pin bool) error {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return sql.ErrNoRows
	}
	if id == "" || len(id) > 256 {
		return errDashboardPinInvalid
	}
	if _, ok := h.dashboardPinNav(ctx)[id]; pin && !ok {
		return errDashboardPinInvalid
	}
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if pin {
			var count, existing int
			if e := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(nav_id=?),0) FROM admin_dashboard_pins WHERE user_id=?`, id, u.ID).Scan(&count, &existing); e != nil {
				return e
			}
			if count >= maxDashboardPins && existing == 0 {
				return errDashboardPinLimit
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO admin_dashboard_pins(user_id,nav_id,position,created_at) SELECT ?,?,COALESCE(max(position),-1)+1,? FROM admin_dashboard_pins WHERE user_id=? ON CONFLICT(user_id,nav_id) DO NOTHING`, u.ID, id, db.Now(), u.ID); e != nil {
				return e
			}
		} else {
			result, e := tx.ExecContext(ctx, `DELETE FROM admin_dashboard_pins WHERE user_id=? AND nav_id=?`, u.ID, id)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n == 0 {
				return nil
			}
		}
		if pin {
			return nil
		}
		return restorePinVisibility(ctx, tx, u.ID, id)
	})
}
func (h *handlers) orderDashboardPins(ctx context.Context, ids []string) error {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return sql.ErrNoRows
	}
	allowed := h.dashboardPinNav(ctx)
	seen := map[string]bool{}
	if len(ids) > maxDashboardPins {
		return errDashboardPinInvalid
	}
	for _, id := range ids {
		if _, ok := allowed[id]; !ok || seen[id] {
			return errDashboardPinInvalid
		}
		seen[id] = true
	}
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT nav_id FROM admin_dashboard_pins WHERE user_id=? ORDER BY position,nav_id`, u.ID)
		if e != nil {
			return e
		}
		current := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			current = append(current, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		extras := []string{}
		count := 0
		for _, id := range current {
			if _, ok := allowed[id]; ok {
				count++
				if !seen[id] {
					return errDashboardPinInvalid
				}
			} else {
				extras = append(extras, id)
			}
		}
		if count != len(ids) {
			return errDashboardPinInvalid
		}
		for position, id := range append(slices.Clone(ids), extras...) {
			if _, e = tx.ExecContext(ctx, `UPDATE admin_dashboard_pins SET position=? WHERE user_id=? AND nav_id=?`, position, u.ID, id); e != nil {
				return e
			}
		}
		return nil
	})
}

func (h *handlers) PinnedNavigation(ctx context.Context) ([]string, error) {
	pins, e := h.dashboardPinned(ctx)
	if e != nil {
		return nil, e
	}
	ids := make([]string, 0, len(pins))
	for _, p := range pins {
		ids = append(ids, p.ID)
	}
	return ids, nil
}
