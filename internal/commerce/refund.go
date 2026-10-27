package commerce

import (
	"context"
	"database/sql"

	"github.com/zadstack/gorun/internal/db"
)

// RefundOrder refunds the order's PaymentIntent through Stripe, then marks
// the order refunded and suspends its entitlement. Concurrent admin clicks
// share Idempotency-Key "refund:"+orderID so Stripe returns one Refund; the
// DB update claims the row afterward (RowsAffected==1). A later
// charge.refunded webhook is a no-op once the order is already refunded.
// Stripe is called before the local claim so a provider failure leaves the
// order refundable for a retry.
func (s *service) RefundOrder(ctx context.Context, orderID string) error {
	o, err := s.Order(ctx, orderID)
	if err != nil {
		return err
	}
	if o.StripePaymentIntent == "" || o.Status == OrderRefunded || o.Status == OrderPending || o.Status == OrderCancelled {
		return &ValidationError{Fields: map[string]string{"order": "commerce.error.refund_failed"}}
	}
	key := s.stripeKeyForOrder(ctx, o)
	if key == "" {
		return ErrStripeNotConfigured
	}
	refundID, err := s.refunds.Create(ctx, key, o.StripePaymentIntent, "refund:"+o.ID)
	if err != nil {
		return err
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.nowString()
		res, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'refunded', updated_at = ? WHERE id = ? AND status <> 'refunded'`, now, o.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			s.audit.RecordTx(ctx, tx, "commerce.order.refunded", "order", o.ID, map[string]any{"refund": refundID, "source": "admin"})
			return s.suspendForOrderTx(ctx, tx, o.ID, "refund")
		}
		return nil
	})
}
