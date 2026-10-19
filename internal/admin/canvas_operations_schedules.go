package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"net/http"
	"time"
)

type canvasSchedule struct {
	Enabled   bool   `json:"enabled"`
	NextRunAt string `json:"next_run_at"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

func (h *handlers) canvasSchedules(ctx context.Context, board string) (map[string]canvasSchedule, error) {
	out := map[string]canvasSchedule{}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT node_id,enabled,next_run_at,status FROM admin_canvas_schedules WHERE board_id=?`, board)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var s canvasSchedule
		if e = rows.Scan(&id, &s.Enabled, &s.NextRunAt, &s.Status); e != nil {
			return nil, e
		}
		out[id] = s
	}
	return out, rows.Err()
}
func (h *handlers) canvasScheduleHTTP(w http.ResponseWriter, r *http.Request) {
	b, e := h.canvasOwned(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	if !auth.Can(r.Context(), "content.edit") {
		h.canvasOpsError(w, 403, "forbidden")
		return
	}
	node := chi.URLParam(r, "node")
	var in struct {
		Enabled     bool   `json:"enabled"`
		RequestID   string `json:"request_id"`
		ReviewToken string `json:"review_token"`
	}
	if studioRead(w, r, &in) != nil || !canvasCardID.MatchString(node) {
		h.canvasOpsError(w, 422, "invalid")
		return
	}
	if !in.Enabled {
		_, e = h.d.DB.ExecContext(r.Context(), `UPDATE admin_canvas_schedules SET enabled=0,status='disabled' WHERE board_id=? AND node_id=?`, b.ID, node)
		if e != nil {
			h.canvasError(w, r, e)
			return
		}
		h.aiJSON(w, 200, map[string]any{"schedule": canvasSchedule{Status: "disabled"}})
		return
	}
	interval, target := "", ""
	for _, c := range b.Cards {
		if c.ID == node && c.Kind == "schedule" {
			interval = c.Interval
		}
	}
	for _, edge := range b.Edges {
		if edge.From == node {
			if target != "" {
				h.canvasOpsError(w, 422, "invalid")
				return
			}
			target = edge.To
		}
	}
	var raw string
	e = h.d.DB.QueryRowContext(r.Context(), `SELECT document FROM admin_canvas_reviews WHERE id=? AND board_id=? AND owner_id=? AND expires_at>?`, in.ReviewToken, b.ID, auth.UserFromContext(r.Context()).ID, db.Now()).Scan(&raw)
	var old canvasPrepared
	if e != nil || json.Unmarshal([]byte(raw), &old) != nil || target != old.NodeID || canvasInterval(interval) == 0 {
		h.canvasOpsError(w, 409, "review_expired")
		return
	}
	p, e := h.canvasPrepare(r.Context(), b, target)
	if e != nil || p.Version != old.Version || p.ReviewHash != old.ReviewHash {
		h.canvasOpsError(w, 409, "consent_changed")
		return
	}
	next := time.Now().Add(canvasInterval(interval)).UTC().Format(time.RFC3339)
	_, e = h.d.DB.ExecContext(r.Context(), `INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,?,?,1,?,'enabled') ON CONFLICT(board_id,node_id) DO UPDATE SET provider_node_id=excluded.provider_node_id,interval=excluded.interval,consent_hash=excluded.consent_hash,enabled=1,next_run_at=CASE WHEN admin_canvas_schedules.enabled=1 AND admin_canvas_schedules.consent_hash=excluded.consent_hash THEN admin_canvas_schedules.next_run_at ELSE excluded.next_run_at END,status='enabled'`, b.ID, node, target, interval, p.GraphHash, next)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	schedules, e := h.canvasSchedules(r.Context(), b.ID)
	if e != nil {
		h.canvasError(w, r, e)
		return
	}
	h.aiJSON(w, 200, map[string]any{"schedule": schedules[node]})
}
func (h *handlers) canvasTick(ctx context.Context) error {
	if _, e := h.d.DB.ExecContext(ctx, `DELETE FROM admin_canvas_inbound_deliveries WHERE created_at<?`, time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	if _, e := h.d.DB.ExecContext(ctx, `DELETE FROM admin_canvas_reviews WHERE expires_at<?`, db.Now()); e != nil {
		return e
	}
	// Interrupted external requests are uncertain and must never be repeated automatically.
	if _, e := h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_runs SET status='uncertain',finished_at=? WHERE status='running' AND started_at<?`, db.Now(), time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT s.board_id,s.node_id,s.provider_node_id,s.interval,s.consent_hash,s.next_run_at,b.owner_id FROM admin_canvas_schedules s JOIN admin_canvases b ON b.id=s.board_id WHERE s.enabled=1 AND s.next_run_at<=? LIMIT 100`, db.Now())
	if e != nil {
		return e
	}
	type due struct{ board, node, target, interval, hash, next, owner string }
	items := []due{}
	for rows.Next() {
		var x due
		if e = rows.Scan(&x.board, &x.node, &x.target, &x.interval, &x.hash, &x.next, &x.owner); e != nil {
			rows.Close()
			return e
		}
		items = append(items, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, x := range items {
		u, e := h.users.Get(ctx, x.owner)
		if e != nil || u.Status != auth.StatusActive {
			if _, e = h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id=? AND node_id=?`, x.board, x.node); e != nil {
				return e
			}
			continue
		}
		as := auth.WithUser(ctx, u)
		b, e := h.canvasOwned(as, x.board)
		var p canvasPrepared
		if e == nil {
			p, e = h.canvasPrepare(as, b, x.target)
		}
		if e != nil || p.GraphHash != x.hash {
			if _, e = h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id=? AND node_id=?`, x.board, x.node); e != nil {
				return e
			}
			continue
		}
		next := time.Now().Add(canvasInterval(x.interval)).UTC().Format(time.RFC3339)
		p.ScheduleID = x.node
		e = db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
			// Consume the slot only when its run and queue entry commit together.
			result, err := tx.ExecContext(ctx, `UPDATE admin_canvas_schedules SET next_run_at=? WHERE board_id=? AND node_id=? AND enabled=1 AND consent_hash=? AND next_run_at=?`, next, x.board, x.node, x.hash, x.next)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return nil
			}
			_, err = h.canvasEnqueueTx(as, tx, p, "schedule:"+x.node+":"+x.next)
			return err
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func (h *handlers) canvasScheduleActive(ctx context.Context, p canvasPrepared) bool {
	if p.InboundID != "" && !h.canvasInboundActive(ctx, p) {
		return false
	}
	if p.ScheduleID == "" {
		return true
	}
	var n int
	e := h.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM admin_canvas_schedules WHERE board_id=? AND node_id=? AND enabled=1 AND consent_hash=?`, p.BoardID, p.ScheduleID, p.GraphHash).Scan(&n)
	return e == nil && n == 1
}

func (h *handlers) canvasMetadata(ctx context.Context, board string) (map[string]map[string]string, map[string]canvasSchedule, error) {
	schedules, e := h.canvasSchedules(ctx, board)
	if e != nil {
		return nil, nil, e
	}
	out := map[string]map[string]string{}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT card_id,actor_kind,actor_name,run_id,created_at FROM admin_canvas_provenance WHERE board_id=?`, board)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, name, run, created string
		if e = rows.Scan(&id, &kind, &name, &run, &created); e != nil {
			return nil, nil, e
		}
		out[id] = map[string]string{"actor_kind": kind, "actor_name": name, "run_id": run, "created_at": created}
	}
	return out, schedules, rows.Err()
}

func (h *handlers) canvasInvalidateSchedules(ctx context.Context, b *canvasBoard) error {
	rows, e := h.d.DB.QueryContext(ctx, `SELECT node_id,provider_node_id,consent_hash FROM admin_canvas_schedules WHERE board_id=? AND enabled=1`, b.ID)
	if e != nil {
		return e
	}
	type check struct{ node, target, hash string }
	all := []check{}
	for rows.Next() {
		var c check
		if e = rows.Scan(&c.node, &c.target, &c.hash); e != nil {
			rows.Close()
			return e
		}
		all = append(all, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, c := range all {
		p, e := h.canvasPrepare(ctx, b, c.target)
		if e != nil || p.GraphHash != c.hash {
			if _, e = h.d.DB.ExecContext(ctx, `UPDATE admin_canvas_schedules SET enabled=0,status='needs_review' WHERE board_id=? AND node_id=? AND consent_hash=?`, b.ID, c.node, c.hash); e != nil {
				return e
			}
		}
	}
	return nil
}
