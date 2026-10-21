package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

// SubscriptionEvent is emitted on subscription.activated, subscription.past_due
// and subscription.canceled so other modules and webhook subscribers can react
// without talking to Stripe.
type SubscriptionEvent struct {
	SubscriptionID       string `json:"subscription_id"`
	StripeSubscriptionID string `json:"stripe_subscription_id"`
	CustomerID           string `json:"customer_id"`
	AccountID            string `json:"account_id"`
	Plan                 string `json:"plan"`
	SiteID               string `json:"site_id"`
	Status               string `json:"status"`
	PriceID              string `json:"price_id"`
	CurrentPeriodEnd     string `json:"current_period_end"`
	GraceUntil           string `json:"grace_until,omitempty"`
	Reason               string `json:"reason,omitempty"`
}

// Invoice is one Stripe invoice stored for /account.
type Invoice struct {
	ID               string `json:"id"`
	StripeInvoiceID  string `json:"stripe_invoice_id"`
	HostedInvoiceURL string `json:"hosted_invoice_url"`
	PDFURL           string `json:"pdf_url"`
	Status           string `json:"status"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	CreatedAt        string `json:"created_at"`
}

type subscriptionRow struct {
	ID, CustomerID, OrderID, AccountID, SiteID, Plan string
	StripeSubscriptionID, StripeCustomerID, PriceID  string
	Status, CurrentPeriodEnd, TrialEnd, GraceUntil   string
	Currency                                         string
	CancelAtPeriodEnd, AccessSuspended               int
}

type hookEmit struct {
	name    string
	payload any
}

func accessClass(status string) string {
	switch status {
	case "active", "trialing":
		return "on"
	case "past_due":
		return "grace"
	default:
		return "off"
	}
}

func (s *service) loadSubscription(ctx context.Context, tx *sql.Tx, stripeID string) (subscriptionRow, bool, error) {
	var row subscriptionRow
	err := tx.QueryRowContext(ctx, `SELECT id, customer_id, COALESCE(order_id, ''), account_id, site_id, plan,
		stripe_subscription_id, stripe_customer_id, price_id, status, current_period_end, trial_end, grace_until,
		currency, cancel_at_period_end, access_suspended FROM subscriptions WHERE stripe_subscription_id = ?`, stripeID).
		Scan(&row.ID, &row.CustomerID, &row.OrderID, &row.AccountID, &row.SiteID, &row.Plan, &row.StripeSubscriptionID,
			&row.StripeCustomerID, &row.PriceID, &row.Status, &row.CurrentPeriodEnd, &row.TrialEnd, &row.GraceUntil,
			&row.Currency, &row.CancelAtPeriodEnd, &row.AccessSuspended)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, fmt.Errorf("commerce: load subscription: %w", err)
	}
	return row, true, nil
}

func unixTime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return db.FormatTime(time.Unix(sec, 0).UTC())
}

// applySubscription upserts a Stripe subscription and moves local access.
func (s *service) applySubscription(ctx context.Context, tx *sql.Tx, eventID string, sub stripeSubscription, reason string) ([]hookEmit, error) {
	if sub.ID == "" {
		return nil, nil
	}
	prev, found, err := s.loadSubscription(ctx, tx, sub.ID)
	if err != nil {
		return nil, err
	}
	customerID, accountID, orderID, plan, siteID := prev.CustomerID, prev.AccountID, prev.OrderID, prev.Plan, prev.SiteID
	if sub.Metadata["account_id"] != "" {
		accountID = sub.Metadata["account_id"]
	}
	if sub.Metadata["plan"] != "" {
		plan = sub.Metadata["plan"]
	}
	if sub.Metadata["site_id"] != "" {
		siteID = sub.Metadata["site_id"]
	}
	if sub.Metadata["order_id"] != "" {
		orderID = sub.Metadata["order_id"]
	}
	if customerID == "" && accountID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE user_id = ?`, accountID).Scan(&customerID)
	}
	if customerID == "" && sub.Customer != "" {
		_ = tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE stripe_customer_id = ?`, sub.Customer).Scan(&customerID)
	}
	if customerID == "" && orderID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT customer_id FROM orders WHERE id = ?`, orderID).Scan(&customerID)
	}
	if customerID == "" {
		s.log.Warn("commerce: subscription has no local customer", "subscription", sub.ID)
		return nil, nil
	}
	if err := s.rememberStripeCustomer(ctx, tx, customerID, sub.Customer); err != nil {
		return nil, err
	}
	period := unixTime(sub.periodEnd())
	trialEnd := unixTime(sub.TrialEnd)
	grace := prev.GraceUntil
	suspended := prev.AccessSuspended
	status := sub.Status
	if status == "" {
		status = prev.Status
	}
	if status == "past_due" {
		if grace == "" || prev.Status != "past_due" {
			grace = db.FormatTime(s.now().Add(time.Duration(s.graceDays(ctx)) * 24 * time.Hour))
		}
		suspended = 0
	}
	if accessClass(status) == "on" {
		grace, suspended = "", 0
	}
	now := s.nowString()
	id := prev.ID
	if !found {
		id = db.NewID()
		_, err = tx.ExecContext(ctx, `INSERT INTO subscriptions (id, customer_id, order_id, account_id, site_id, plan,
			stripe_subscription_id, stripe_customer_id, price_id, status, current_period_end, cancel_at_period_end,
			trial_end, grace_until, access_suspended, currency, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, customerID, nullable(orderID), accountID, siteID, plan, sub.ID, sub.Customer, sub.priceID(), status, period,
			boolInt(sub.CancelAtPeriodEnd), trialEnd, grace, suspended, strings.ToUpper(sub.Currency), now, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE subscriptions SET customer_id = ?, order_id = ?, account_id = ?, site_id = ?, plan = ?,
			stripe_customer_id = ?, price_id = ?, status = ?, current_period_end = ?, cancel_at_period_end = ?, trial_end = ?,
			grace_until = ?, access_suspended = ?, currency = ?, updated_at = ? WHERE id = ?`,
			customerID, nullable(orderID), accountID, siteID, plan, sub.Customer, sub.priceID(), status, period,
			boolInt(sub.CancelAtPeriodEnd), trialEnd, grace, suspended, strings.ToUpper(firstNonEmpty(sub.Currency, prev.Currency)), now, id)
	}
	if err != nil {
		return nil, fmt.Errorf("commerce: save subscription: %w", err)
	}
	s.audit.RecordTx(ctx, tx, "commerce.subscription.save", "subscription", id, map[string]any{
		"event": eventID, "status": status, "stripe_subscription_id": sub.ID, "reason": reason,
	})
	row := subscriptionRow{ID: id, CustomerID: customerID, OrderID: orderID, AccountID: accountID, SiteID: siteID, Plan: plan,
		StripeSubscriptionID: sub.ID, PriceID: sub.priceID(), Status: status, CurrentPeriodEnd: period, GraceUntil: grace, AccessSuspended: suspended}
	hooks, err := s.syncAccess(ctx, tx, prev.Status, row, eventID, reason)
	return hooks, err
}

func (s *service) syncAccess(ctx context.Context, tx *sql.Tx, prevStatus string, row subscriptionRow, eventID, reason string) ([]hookEmit, error) {
	next := accessClass(row.Status)
	if row.AccessSuspended == 1 {
		next = "off"
	}
	prev := accessClass(prevStatus)
	if err := s.applyEntitlementAccess(ctx, tx, row, next, eventID); err != nil {
		return nil, err
	}
	if next == prev && reason != "grace_expired" {
		return nil, nil
	}
	ev := SubscriptionEvent{SubscriptionID: row.ID, StripeSubscriptionID: row.StripeSubscriptionID, CustomerID: row.CustomerID,
		AccountID: row.AccountID, Plan: row.Plan, SiteID: row.SiteID, Status: row.Status, PriceID: row.PriceID,
		CurrentPeriodEnd: row.CurrentPeriodEnd, GraceUntil: row.GraceUntil, Reason: reason}
	switch next {
	case "on":
		return []hookEmit{{HookSubActivated, ev}}, nil
	case "grace":
		if err := s.enqueueBillingMail(ctx, tx, row, "payment_failed"); err != nil {
			return nil, err
		}
		return []hookEmit{{HookSubPastDue, ev}}, nil
	default:
		return []hookEmit{{HookSubCanceled, ev}}, nil
	}
}

func (s *service) applyEntitlementAccess(ctx context.Context, tx *sql.Tx, row subscriptionRow, class, eventID string) error {
	if row.OrderID == "" {
		return nil
	}
	var entID, status, externalID string
	err := tx.QueryRowContext(ctx, `SELECT id, status, external_id FROM entitlements WHERE order_id = ?`, row.OrderID).Scan(&entID, &status, &externalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commerce: subscription entitlement: %w", err)
	}
	now := s.nowString()
	switch class {
	case "on":
		end := row.CurrentPeriodEnd
		if end == "" {
			end = statusEnd(now)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'active', ends_at = ?, updated_at = ? WHERE id = ?`, end, now, entID); err != nil {
			return err
		}
		if status == EntitlementSuspended && externalID != "" {
			key := JobResume + ":" + eventID + ":" + entID
			_, err = s.d.Jobs.EnqueueTx(ctx, tx, JobResume, suspendPayload{EntitlementID: entID, Reason: "billing", Key: "billing:" + eventID + ":" + entID, JobKey: key}, jobs.Idempotency(key))
			return err
		}
	case "grace":
		return nil
	default:
		if status == EntitlementSuspended || status == EntitlementExpired || status == EntitlementFailed {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'suspended', updated_at = ? WHERE id = ?`, now, entID); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "commerce.entitlement.suspend", "entitlement", entID, map[string]any{"reason": "billing", "order": row.OrderID})
		return s.enqueueSuspendTx(ctx, tx, entID, "billing:"+eventID)
	}
	return nil
}

func statusEnd(now string) string { return now }

func (s *service) enqueueBillingMail(ctx context.Context, tx *sql.Tx, row subscriptionRow, kind string) error {
	var email, locale string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(u.email, ''), COALESCE(u.locale, '') FROM customers c LEFT JOIN users u ON u.id = c.user_id WHERE c.id = ?`, row.CustomerID).Scan(&email, &locale)
	if err != nil || email == "" {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	key := JobBillingMail + ":" + kind + ":" + row.ID + ":" + row.Status
	_, err = s.d.Jobs.EnqueueTx(ctx, tx, JobBillingMail, billingMailPayload{Kind: kind, Email: email, Locale: locale, Plan: row.Plan, Link: s.baseURL(ctx) + "/account"}, jobs.Idempotency(key))
	return err
}

type billingMailPayload struct {
	Kind, Email, Locale, Plan, Link string
}

func (s *service) billingMail(ctx context.Context, raw json.RawMessage) error {
	var p billingMailPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Email == "" {
		return nil
	}
	if p.Locale == "" {
		p.Locale = s.siteLocale(ctx)
	}
	var subject, html, text string
	kind := "past_due"
	switch p.Kind {
	case "trial_ending":
		kind = "trial"
	case "suspended":
		kind = "suspended"
	}
	subject, html, text = billingEmail(s.d.I18n, p.Locale, kind, p.Plan, p.Link)
	if err := s.sendMail(ctx, p.Email, subject, html, text); err != nil {
		return err
	}
	return nil
}

// expireGrace suspends access whose grace window has ended.
func (s *service) expireGrace(ctx context.Context) error {
	now := s.nowString()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM subscriptions WHERE status = 'past_due' AND access_suspended = 0 AND grace_until <> '' AND grace_until <= ?`, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		var hooks []hookEmit
		err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
			var row subscriptionRow
			err := tx.QueryRowContext(ctx, `SELECT id, customer_id, COALESCE(order_id, ''), account_id, site_id, plan, stripe_subscription_id,
				price_id, status, current_period_end, grace_until FROM subscriptions WHERE id = ?`, id).
				Scan(&row.ID, &row.CustomerID, &row.OrderID, &row.AccountID, &row.SiteID, &row.Plan, &row.StripeSubscriptionID,
					&row.PriceID, &row.Status, &row.CurrentPeriodEnd, &row.GraceUntil)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE subscriptions SET access_suspended = 1, updated_at = ? WHERE id = ? AND access_suspended = 0`, s.nowString(), id); err != nil {
				return err
			}
			row.AccessSuspended = 1
			hooks, err = s.syncAccess(ctx, tx, "past_due", row, "grace:"+id, "grace_expired")
			if err != nil {
				return err
			}
			return s.enqueueBillingMail(ctx, tx, row, "suspended")
		})
		if err != nil {
			return err
		}
		for _, h := range hooks {
			s.d.Hooks.Emit(ctx, h.name, h.payload)
		}
	}
	return nil
}

// RequirePlan reports ErrPlanRequired unless accountID may use plan.
func (s *service) RequirePlan(ctx context.Context, accountID, plan string) error {
	ok, err := s.planAllowed(ctx, accountID, plan)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlanRequired
	}
	return nil
}

// RequirePlan looks up the commerce service. Plugins and later modules call it
// instead of reimplementing billing.
func RequirePlan(ctx context.Context, d *kernel.Deps, accountID, plan string) error {
	svc, ok := kernel.Get[Service](d, kernel.ServiceCommerce)
	if !ok {
		return ErrPlanRequired
	}
	return svc.RequirePlan(ctx, accountID, plan)
}

func (s *service) planAllowed(ctx context.Context, accountID, plan string) (bool, error) {
	if strings.TrimSpace(accountID) == "" {
		return false, nil
	}
	now := s.nowString()
	q := `SELECT status, grace_until, access_suspended FROM subscriptions WHERE (account_id = ? OR customer_id IN (SELECT id FROM customers WHERE user_id = ?))`
	args := []any{accountID, accountID}
	if plan != "" {
		q += ` AND plan = ?`
		args = append(args, plan)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, grace string
		var suspended int
		if err := rows.Scan(&status, &grace, &suspended); err != nil {
			return false, err
		}
		switch accessClass(status) {
		case "on":
			return true, nil
		case "grace":
			if suspended == 0 && (grace == "" || grace > now) {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

func (s *service) customerInvoices(ctx context.Context, customerID string) ([]Invoice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, stripe_invoice_id, hosted_invoice_url, pdf_url, status, amount_minor, currency, created_at
		FROM invoices WHERE customer_id = ? ORDER BY created_at DESC LIMIT 50`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invoice{}
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.StripeInvoiceID, &inv.HostedInvoiceURL, &inv.PDFURL, &inv.Status, &inv.AmountMinor, &inv.Currency, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *service) saveInvoice(ctx context.Context, tx *sql.Tx, customerID, subscriptionID string, inv stripeInvoice) error {
	if inv.ID == "" || customerID == "" {
		return nil
	}
	amount := inv.AmountPaid
	if amount == 0 {
		amount = inv.AmountDue
	}
	now := s.nowString()
	_, err := tx.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, subscription_id, stripe_invoice_id, hosted_invoice_url, pdf_url, status, amount_minor, currency, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (stripe_invoice_id) DO UPDATE SET hosted_invoice_url = excluded.hosted_invoice_url, pdf_url = excluded.pdf_url,
		status = excluded.status, amount_minor = excluded.amount_minor, currency = excluded.currency`,
		db.NewID(), customerID, subscriptionID, inv.ID, inv.HostedInvoiceURL, inv.InvoicePDF, inv.Status, amount, strings.ToUpper(inv.Currency), now)
	if err != nil {
		return fmt.Errorf("commerce: save invoice: %w", err)
	}
	return nil
}
