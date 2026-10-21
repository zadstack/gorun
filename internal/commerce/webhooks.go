package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

const maxWebhookBody = 1 << 20

// Handled Stripe event types.
const (
	evCheckoutCompleted    = "checkout.session.completed"
	evCheckoutAsyncSuccess = "checkout.session.async_payment_succeeded"
	evCheckoutAsyncFailed  = "checkout.session.async_payment_failed"
	evCheckoutExpired      = "checkout.session.expired"
	evChargeRefunded       = "charge.refunded"
	evSubCreated           = "customer.subscription.created"
	evSubUpdated           = "customer.subscription.updated"
	evSubDeleted           = "customer.subscription.deleted"
	evSubTrialWillEnd      = "customer.subscription.trial_will_end"
	evInvoicePaid          = "invoice.paid"
	evInvoicePaymentFailed = "invoice.payment_failed"
)

type sessionObject struct {
	ID                string            `json:"id"`
	ClientReferenceID string            `json:"client_reference_id"`
	PaymentStatus     string            `json:"payment_status"`
	PaymentIntent     string            `json:"payment_intent"`
	Customer          string            `json:"customer"`
	Mode              string            `json:"mode"`
	AmountTotal       *int64            `json:"amount_total"`
	Currency          string            `json:"currency"`
	Metadata          map[string]string `json:"metadata"`
}

type chargeObject struct {
	ID            string            `json:"id"`
	PaymentIntent string            `json:"payment_intent"`
	Refunded      bool              `json:"refunded"`
	Metadata      map[string]string `json:"metadata"`
}

// paidEvent is emitted as commerce.order.paid after commit.
type paidEvent struct {
	OrderID    string `json:"order_id"`
	Number     string `json:"number"`
	Email      string `json:"email"`
	TotalMinor int64  `json:"total_minor"`
	Currency   string `json:"currency"`
}

// HandleStripeEvent verifies the Stripe-Signature header (webhook secret
// from settings or config; unsigned payloads are always rejected), records
// the event id in stripe_events and processes it in the same transaction,
// so a replayed event is a no-op:
//   - checkout.session.completed / async_payment_succeeded: a pending order
//     whose session matches exactly (session id, mode=payment,
//     payment_status=paid, amount_total, currency, metadata.order_id) → paid,
//     commerce.provision enqueued in that transaction; a mismatch is audited
//     (commerce.order.mismatch) and never pays;
//   - checkout.session.expired → cancelled; async_payment_failed → failed;
//   - charge.refunded (full): order → refunded, entitlement suspended and
//     commerce.suspend enqueued; a refund that precedes the completion is
//     remembered (refunded_pending) so the completion never provisions.
//
// Other event types are recorded and ignored.
func (s *service) HandleStripeEvent(ctx context.Context, payload []byte, sigHeader string) error {
	return s.handleStripeEvent(ctx, payload, sigHeader, "")
}

func (s *service) handleStripeEvent(ctx context.Context, payload []byte, sigHeader, mode string) error {
	secret := s.webhookSecret(ctx)
	if mode == "test" || mode == "live" {
		secret = s.webhookSecretFor(ctx, mode)
	}
	if secret == "" {
		return ErrStripeNotConfigured
	}
	// The signature is verified as usual; the API version check is relaxed
	// because Dashboard endpoints often run a different version and only a
	// few stable fields of data.object are read.
	ev, err := webhook.ConstructEventWithOptions(payload, sigHeader, secret, webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if ev.ID == "" || ev.Data == nil {
		return fmt.Errorf("%w: event without id or data", ErrInvalidSignature)
	}
	var paid *paidEvent
	var hooks []hookEmit
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO stripe_events (id, type, received_at, payload_json) VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
			ev.ID, string(ev.Type), s.nowString(), string(payload))
		if err != nil {
			return fmt.Errorf("commerce: record stripe event: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // duplicate delivery: already processed
		}
		switch string(ev.Type) {
		case evCheckoutCompleted, evCheckoutAsyncSuccess:
			paid, err = s.markPaid(ctx, tx, ev.ID, ev.Data.Raw)
		case evCheckoutExpired:
			err = s.closeSession(ctx, tx, ev.ID, ev.Data.Raw, OrderCancelled, "commerce.order.cancelled")
		case evCheckoutAsyncFailed:
			err = s.closeSession(ctx, tx, ev.ID, ev.Data.Raw, OrderFailed, "commerce.order.payment_failed")
		case evChargeRefunded:
			err = s.markRefunded(ctx, tx, ev.ID, ev.Data.Raw)
		case evSubCreated, evSubUpdated, evSubDeleted:
			hooks, err = s.onSubscription(ctx, tx, ev.ID, string(ev.Type), ev.Data.Raw)
		case evSubTrialWillEnd:
			err = s.onTrialWillEnd(ctx, tx, ev.Data.Raw)
		case evInvoicePaid, evInvoicePaymentFailed:
			hooks, err = s.onInvoice(ctx, tx, ev.ID, string(ev.Type), ev.Data.Raw)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE stripe_events SET processed_at = ? WHERE id = ?`, s.nowString(), ev.ID)
		return err
	})
	if err != nil {
		return err
	}
	if paid != nil {
		s.d.Hooks.Emit(ctx, HookOrderPaid, paid)
	}
	for _, h := range hooks {
		s.d.Hooks.Emit(ctx, h.name, h.payload)
	}
	return nil
}

type stripeSubscription struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	CurrentPeriodEnd  int64             `json:"current_period_end"`
	TrialEnd          int64             `json:"trial_end"`
	Currency          string            `json:"currency"`
	Metadata          map[string]string `json:"metadata"`
	CustomerRaw       json.RawMessage   `json:"customer"`
	Items             struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
			Price            struct {
				ID       string `json:"id"`
				Currency string `json:"currency"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
	Customer string `json:"-"`
}

func (s stripeSubscription) periodEnd() int64 {
	if s.CurrentPeriodEnd > 0 {
		return s.CurrentPeriodEnd
	}
	if len(s.Items.Data) > 0 {
		return s.Items.Data[0].CurrentPeriodEnd
	}
	return 0
}

func (s stripeSubscription) priceID() string {
	if len(s.Items.Data) > 0 {
		return s.Items.Data[0].Price.ID
	}
	return ""
}

type stripeInvoice struct {
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	HostedInvoiceURL string          `json:"hosted_invoice_url"`
	InvoicePDF       string          `json:"invoice_pdf"`
	Currency         string          `json:"currency"`
	AmountPaid       int64           `json:"amount_paid"`
	AmountDue        int64           `json:"amount_due"`
	CustomerRaw      json.RawMessage `json:"customer"`
	SubscriptionRaw  json.RawMessage `json:"subscription"`
	Customer         string          `json:"-"`
	Subscription     string          `json:"-"`
}

func flexID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var obj struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &obj)
	return obj.ID
}

func (s *service) onSubscription(ctx context.Context, tx *sql.Tx, eventID, typ string, raw json.RawMessage) ([]hookEmit, error) {
	var sub stripeSubscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, fmt.Errorf("commerce: decode subscription: %w", err)
	}
	sub.Customer = flexID(sub.CustomerRaw)
	if sub.Currency == "" && len(sub.Items.Data) > 0 {
		sub.Currency = sub.Items.Data[0].Price.Currency
	}
	if typ == evSubDeleted {
		sub.Status = "canceled"
	}
	reason := "subscription"
	if typ == evSubTrialWillEnd {
		reason = "trial_will_end"
	}
	return s.applySubscription(ctx, tx, eventID, sub, reason)
}

func (s *service) onTrialWillEnd(ctx context.Context, tx *sql.Tx, raw json.RawMessage) error {
	var sub stripeSubscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return fmt.Errorf("commerce: decode trial: %w", err)
	}
	sub.Customer = flexID(sub.CustomerRaw)
	row, found, err := s.loadSubscription(ctx, tx, sub.ID)
	if err != nil || !found {
		return err
	}
	return s.enqueueBillingMail(ctx, tx, row, "trial_ending")
}

func (s *service) onInvoice(ctx context.Context, tx *sql.Tx, eventID, typ string, raw json.RawMessage) ([]hookEmit, error) {
	var inv stripeInvoice
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, fmt.Errorf("commerce: decode invoice: %w", err)
	}
	inv.Customer = flexID(inv.CustomerRaw)
	inv.Subscription = flexID(inv.SubscriptionRaw)
	row, found, err := s.loadSubscription(ctx, tx, inv.Subscription)
	if err != nil {
		return nil, err
	}
	customerID := ""
	if found {
		customerID = row.CustomerID
	}
	if customerID == "" && inv.Customer != "" {
		_ = tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE stripe_customer_id = ?`, inv.Customer).Scan(&customerID)
	}
	subID := ""
	if found {
		subID = row.ID
	}
	if err := s.saveInvoice(ctx, tx, customerID, subID, inv); err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	status := row.Status
	if typ == evInvoicePaid {
		status = "active"
	} else {
		status = "past_due"
	}
	sub := stripeSubscription{ID: row.StripeSubscriptionID, Customer: inv.Customer, Status: status, Currency: inv.Currency,
		Metadata: map[string]string{"account_id": row.AccountID, "plan": row.Plan, "site_id": row.SiteID, "order_id": row.OrderID}}
	if row.CurrentPeriodEnd != "" {
		if t, err := db.ParseTime(row.CurrentPeriodEnd); err == nil {
			sub.CurrentPeriodEnd = t.Unix()
		}
	}
	sub.Items.Data = []struct {
		CurrentPeriodEnd int64 `json:"current_period_end"`
		Price            struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
		} `json:"price"`
	}{{Price: struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
	}{ID: row.PriceID}}}
	return s.applySubscription(ctx, tx, eventID, sub, typ)
}

// sessionOrder resolves the order a checkout session refers to:
// client_reference_id, else metadata.order_id. Both present and different
// is a mismatch (ok = false).
func sessionOrder(obj sessionObject) (id string, ok bool) {
	meta := strings.TrimSpace(obj.Metadata["order_id"])
	switch {
	case obj.ClientReferenceID != "" && meta != "" && meta != obj.ClientReferenceID:
		return obj.ClientReferenceID, false
	case obj.ClientReferenceID != "":
		return obj.ClientReferenceID, true
	default:
		return meta, true
	}
}

// sessionMismatch returns why a paid session does not match order o, or "".
func sessionMismatch(obj sessionObject, o *Order, refOK bool) string {
	switch {
	case o.ProductSnapshot["payment_method"] == "manual_request":
		return "manual requests do not have a Stripe payment session"
	case !refOK:
		return "metadata.order_id differs from client_reference_id"
	case o.StripeSessionID != "" && obj.ID != o.StripeSessionID:
		return "session id differs from the order's session"
	case obj.Mode == "subscription" && billingModeOf(o) != "subscription":
		return "mode is not payment"
	case obj.Mode == "payment" && billingModeOf(o) == "subscription":
		return "mode is not subscription"
	case obj.Mode != "payment" && obj.Mode != "subscription":
		return "mode is not payment"
	case obj.Mode == "payment" && (obj.AmountTotal == nil || *obj.AmountTotal != o.TotalMinor):
		return "amount_total differs from the order total"
	case obj.Mode == "subscription" && obj.AmountTotal != nil && *obj.AmountTotal != 0 && *obj.AmountTotal != o.TotalMinor:
		return "amount_total differs from the order total"
	case !strings.EqualFold(obj.Currency, o.Currency):
		return "currency differs from the order currency"
	}
	return ""
}

// markPaid moves a pending order to paid and enqueues provisioning, once,
// but only for a session that matches the order exactly.
func (s *service) markPaid(ctx context.Context, tx *sql.Tx, eventID string, raw json.RawMessage) (*paidEvent, error) {
	var obj sessionObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("commerce: decode checkout session: %w", err)
	}
	if obj.Mode == "subscription" {
		if obj.PaymentStatus != "paid" && obj.PaymentStatus != "no_payment_required" {
			return nil, nil
		}
	} else if obj.PaymentStatus != "paid" {
		return nil, nil // e.g. delayed payment methods: wait for async_payment_succeeded
	}
	orderID, refOK := sessionOrder(obj)
	if orderID == "" {
		return nil, nil
	}
	o, err := getOrder(ctx, tx, orderID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A redelivery of the session that already paid the order is a no-op.
	if o.Status != OrderPending && o.PaidAt != "" && o.StripeSessionID == obj.ID {
		return nil, nil
	}
	if why := sessionMismatch(obj, o, refOK); why != "" {
		var amount any
		if obj.AmountTotal != nil {
			amount = *obj.AmountTotal
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.mismatch", "order", o.ID, map[string]any{"event": eventID, "session": obj.ID,
			"expected_session": o.StripeSessionID, "amount_total": amount, "total_minor": o.TotalMinor, "currency": obj.Currency,
			"order_currency": o.Currency, "mode": obj.Mode, "reason": why})
		s.log.Warn("commerce: checkout session does not match order", "order", o.ID, "session", obj.ID, "reason", why)
		return nil, nil
	}
	if o.Status != OrderPending {
		// A matching paid completion for a closed order: money was taken
		// that nothing will provision. Flag it for an admin.
		var amount any
		if obj.AmountTotal != nil {
			amount = *obj.AmountTotal
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.paid_closed", "order", o.ID, map[string]any{"event": eventID, "session": obj.ID,
			"status": o.Status, "payment_intent": obj.PaymentIntent, "amount_total": amount, "currency": obj.Currency})
		s.log.Warn("commerce: paid completion for a non-pending order", "order", o.ID, "status", o.Status, "session", obj.ID)
		return nil, nil
	}
	now := s.nowString()
	refundedFirst, err := s.refundSeen(ctx, tx, o.ID, obj.PaymentIntent)
	if err != nil {
		return nil, err
	}
	if refundedFirst {
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'refunded', paid_at = ?, stripe_session_id = ?, stripe_payment_intent = ?, updated_at = ?
			WHERE id = ? AND status = 'pending'`, now, obj.ID, obj.PaymentIntent, now, o.ID); err != nil {
			return nil, fmt.Errorf("commerce: mark refunded order: %w", err)
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.refunded", "order", o.ID, map[string]any{"event": eventID, "session": obj.ID, "refund_before_payment": true})
		return nil, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'paid', paid_at = ?, stripe_session_id = ?, stripe_payment_intent = ?, updated_at = ?
		WHERE id = ? AND status = 'pending'`, now, obj.ID, obj.PaymentIntent, now, o.ID)
	if err != nil {
		return nil, fmt.Errorf("commerce: mark order paid: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, nil
	}
	if err := s.rememberStripeCustomer(ctx, tx, o.CustomerID, obj.Customer); err != nil {
		return nil, err
	}
	if o.snapshot().Kind == "" || o.snapshot().Kind == ProductProvisioning {
		key := JobProvision + ":" + o.ID
		if _, err := s.d.Jobs.EnqueueTx(ctx, tx, JobProvision, provisionPayload{OrderID: o.ID, JobKey: key}, jobs.Idempotency(key)); err != nil {
			return nil, err
		}
	}
	s.audit.RecordTx(ctx, tx, "commerce.order.paid", "order", o.ID, map[string]any{"number": o.Number, "event": eventID, "session": obj.ID})
	return &paidEvent{OrderID: o.ID, Number: o.Number, Email: o.Email, TotalMinor: o.TotalMinor, Currency: o.Currency}, nil
}

// refundSeen reports a full refund recorded before the completion: the
// order's refunded_pending flag, or a charge.refunded event for the same
// payment intent.
func (s *service) refundSeen(ctx context.Context, tx *sql.Tx, orderID, paymentIntent string) (bool, error) {
	var flag int
	if err := tx.QueryRowContext(ctx, `SELECT refunded_pending FROM orders WHERE id = ?`, orderID).Scan(&flag); err != nil {
		return false, fmt.Errorf("commerce: refund flag: %w", err)
	}
	if flag == 1 || paymentIntent == "" {
		return flag == 1, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM stripe_events WHERE type = 'charge.refunded'
		AND json_extract(payload_json, '$.data.object.payment_intent') = ? AND json_extract(payload_json, '$.data.object.refunded') = 1`, paymentIntent).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("commerce: earlier refunds: %w", err)
	}
	return n > 0, nil
}

// closeSession handles an expired or failed checkout session: the pending
// order (with a matching session id) becomes status, audited as action.
func (s *service) closeSession(ctx context.Context, tx *sql.Tx, eventID string, raw json.RawMessage, status, action string) error {
	var obj sessionObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("commerce: decode checkout session: %w", err)
	}
	orderID, refOK := sessionOrder(obj)
	if orderID == "" || !refOK {
		return nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status = ?, updated_at = ? WHERE id = ? AND status = 'pending'
		AND (stripe_session_id = '' OR stripe_session_id = ?)`, status, s.nowString(), orderID, obj.ID)
	if err != nil {
		return fmt.Errorf("commerce: close order: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		s.audit.RecordTx(ctx, tx, action, "order", orderID, map[string]any{"event": eventID, "session": obj.ID})
	}
	return nil
}

// markRefunded refunds the order paid by the charge's payment intent and
// suspends its entitlement (the server call runs in the commerce.suspend
// job). A refund for an order not paid yet sets refunded_pending.
func (s *service) markRefunded(ctx context.Context, tx *sql.Tx, eventID string, raw json.RawMessage) error {
	var obj chargeObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("commerce: decode charge: %w", err)
	}
	if !obj.Refunded {
		return nil // partial refunds keep access
	}
	var orderID, status string
	err := sql.ErrNoRows
	if obj.PaymentIntent != "" {
		err = tx.QueryRowContext(ctx, `SELECT id, status FROM orders WHERE stripe_payment_intent = ?`, obj.PaymentIntent).Scan(&orderID, &status)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// The completion has not arrived yet: remember the refund on the
		// order when the charge names it (otherwise markPaid finds this event
		// by payment intent).
		if id := obj.Metadata["order_id"]; id != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE orders SET refunded_pending = 1, updated_at = ? WHERE id = ? AND status = 'pending'`, s.nowString(), id); err != nil {
				return fmt.Errorf("commerce: flag refund: %w", err)
			}
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("commerce: find refunded order: %w", err)
	}
	if status == OrderRefunded {
		return nil
	}
	now := s.nowString()
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'refunded', updated_at = ? WHERE id = ?`, now, orderID); err != nil {
		return fmt.Errorf("commerce: mark refunded: %w", err)
	}
	s.audit.RecordTx(ctx, tx, "commerce.order.refunded", "order", orderID, map[string]any{"event": eventID, "charge": obj.ID})
	return s.suspendForOrderTx(ctx, tx, orderID, "refund")
}

// suspendForOrderTx marks the order's entitlement suspended (if any, and
// not already ended) and enqueues the server-side suspension.
func (s *service) suspendForOrderTx(ctx context.Context, tx *sql.Tx, orderID, reason string) error {
	var entID, status string
	err := tx.QueryRowContext(ctx, `SELECT id, status FROM entitlements WHERE order_id = ?`, orderID).Scan(&entID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // not provisioned yet: the provision job sees the refund and stops
	}
	if err != nil {
		return fmt.Errorf("commerce: find entitlement: %w", err)
	}
	if status == EntitlementSuspended || status == EntitlementExpired || status == EntitlementFailed {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'suspended', updated_at = ? WHERE id = ?`, s.nowString(), entID); err != nil {
		return fmt.Errorf("commerce: suspend entitlement: %w", err)
	}
	s.audit.RecordTx(ctx, tx, "commerce.entitlement.suspend", "entitlement", entID, map[string]any{"reason": reason, "order": orderID})
	return s.enqueueSuspendTx(ctx, tx, entID, reason)
}

// enqueueSuspendTx enqueues the commerce.suspend job for entID, once per reason.
func (s *service) enqueueSuspendTx(ctx context.Context, tx *sql.Tx, entID, reason string) error {
	key := JobSuspend + ":" + reason + ":" + entID
	_, err := s.d.Jobs.EnqueueTx(ctx, tx, JobSuspend, suspendPayload{EntitlementID: entID, Reason: reason, Key: reason + ":" + entID, JobKey: key},
		jobs.Idempotency(key))
	return err
}

func testSubscriptionEvent(secret string, now time.Time) ([]byte, string) {
	ev := map[string]any{"id": "evt_test_" + now.UTC().Format("20060102150405"), "object": "event", "api_version": "2025-03-31.basil",
		"type": evSubUpdated, "created": now.Unix(), "livemode": false,
		"data": map[string]any{"object": map[string]any{"id": "sub_test_event", "object": "subscription", "status": "active",
			"customer": "cus_test_event", "metadata": map[string]string{}}}}
	body, _ := json.Marshal(ev)
	sp := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: secret, Timestamp: now})
	return body, sp.Header
}

func billingModeOf(o *Order) string {
	if o.snapshot().BillingMode == "subscription" {
		return "subscription"
	}
	return "payment"
}

// handleStripeWebhook is POST /webhooks/stripe (the active test/live secret).
func (h *publicHandlers) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	h.finishStripeWebhook(w, r, "")
}

func (h *publicHandlers) handleStripeWebhookMode(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.finishStripeWebhook(w, r, mode) }
}

func (h *publicHandlers) finishStripeWebhook(w http.ResponseWriter, r *http.Request, mode string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	err = h.svc.handleStripeEvent(r.Context(), body, r.Header.Get("Stripe-Signature"), mode)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	case errors.Is(err, ErrInvalidSignature):
		h.svc.log.Warn("commerce: stripe webhook rejected", "err", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
	case errors.Is(err, ErrStripeNotConfigured):
		http.Error(w, "webhook secret not configured", http.StatusServiceUnavailable)
	default:
		h.svc.log.Error("commerce: stripe webhook", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError) // Stripe retries
	}
}
