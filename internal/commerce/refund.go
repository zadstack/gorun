package commerce

import (
	"context"
	"database/sql"

	"github.com/zadstack/gorun/internal/db"
)

// RefundOrder claims the order as refunded, then refunds the PaymentIntent
// through Stripe. The DB claim runs first so concurrent admin clicks cannot
// each create a Stripe refund; Stripe also receives Idempotency-Key
// "refund:"+orderID as a second line of defence. A later charge.refunded
// webhook is a no-op once the order is already refunded.
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
	claimed := false
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.nowString()
		res, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'refunded', updated_at = ? WHERE id = ? AND status <> 'refunded' AND status <> 'pending' AND status <> 'cancelled' AND stripe_payment_intent <> ''`, now, o.ID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil
		}
		claimed = true
		return s.suspendForOrderTx(ctx, tx, o.ID, "refund")
	})
	if err != nil {
		return err
	}
	if !claimed {
		return &ValidationError{Fields: map[string]string{"order": "commerce.error.refund_failed"}}
	}
	refundID, err := s.refunds.Create(ctx, key, o.StripePaymentIntent, "refund:"+o.ID)
	if err != nil {
		// Order is already refunded locally; surface the Stripe error so an
		// operator can retry (Stripe idempotency key will dedupe).
		s.audit.Record(ctx, "commerce.order.refunded", "order", o.ID, map[string]any{"source": "admin", "stripe_error": true})
		return err
	}
	s.audit.Record(ctx, "commerce.order.refunded", "order", o.ID, map[string]any{"refund": refundID, "source": "admin"})
	return nil
}
