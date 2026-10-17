package commerce

import (
	"context"
	"database/sql"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

type RequestUpdate struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// UpdateRequest tracks human service fulfillment independently of financial
// status. It cannot mark an order paid or grant a digital download.
func (s *service) UpdateRequest(ctx context.Context, id string, req RequestUpdate) (*Order, error) {
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 4000 {
		return nil, cartError("note")
	}
	valid := map[string]bool{"accepted": true, "in_progress": true, "fulfilled": true, "cancelled": true}
	if !valid[req.Status] {
		return nil, cartError("status")
	}
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		o, err := getOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		isManual := o.ProductSnapshot["payment_method"] == "manual_request"
		isPaidGeneric := o.PaidAt != "" && (o.Status == OrderPaid || o.Status == OrderActive) && (o.snapshot().Kind == ProductService || o.snapshot().Kind == ProductDigital)
		if (!isManual && !isPaidGeneric) || o.RequestStatus == "fulfilled" || o.RequestStatus == "cancelled" {
			return cartError("status")
		}
		allowed := map[string]map[string]bool{"pending": {"accepted": true, "cancelled": true}, "accepted": {"in_progress": true, "fulfilled": true, "cancelled": true}, "in_progress": {"fulfilled": true, "cancelled": true}}
		if !allowed[o.RequestStatus][req.Status] {
			return cartError("status")
		}
		if req.Status == "fulfilled" {
			var digital int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM order_items WHERE order_id=? AND json_extract(snapshot_json,'$.kind')='digital'`, id).Scan(&digital); err != nil {
				return err
			}
			if digital > 0 && o.PaidAt == "" {
				return cartError("digital_payment_required")
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE orders SET request_status=?,fulfillment_note=?,updated_at=? WHERE id=?`, req.Status, req.Note, s.nowString(), id)
		if err != nil {
			return err
		}
		if req.Status == "cancelled" {
			if _, err = tx.ExecContext(ctx, `UPDATE orders SET status='cancelled' WHERE id=? AND status='pending'`, id); err != nil {
				return err
			}
		}
		s.audit.RecordTx(ctx, tx, "commerce.request.update", "order", id, map[string]any{"from": o.RequestStatus, "to": req.Status})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Order(ctx, id)
}
