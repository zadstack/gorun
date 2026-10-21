package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

const (
	customerLinkTTL = 24 * time.Hour
	expireEvery     = 24 * time.Hour
)

type provisionPayload struct {
	OrderID string `json:"order_id"`
	Retry   int    `json:"retry,omitempty"`   // admin retries: create op key gets ":retryN"
	JobKey  string `json:"job_key,omitempty"` // the job's idempotency key (informational; attempts come from jobs.AttemptFromContext)
}

type suspendPayload struct {
	EntitlementID string `json:"entitlement_id"`
	Reason        string `json:"reason"` // refund | expire
	Key           string `json:"key"`
	JobKey        string `json:"job_key,omitempty"`
}

// activatedEvent is emitted as commerce.entitlement.activated.
type activatedEvent struct {
	EntitlementID string `json:"entitlement_id"`
	OrderID       string `json:"order_id"`
	CustomerID    string `json:"customer_id"`
	PlanCode      string `json:"plan_code"`
	ServerID      string `json:"server_id"`
	EndsAt        string `json:"ends_at"`
}

// accountResult is the data of POST /accounts (create or extend).
type accountResult struct {
	Status    string `json:"status"`
	PeriodEnd any    `json:"period_end"` // string or epoch number
}

// accountRequest is the POST /accounts body, field for field (and in the
// same order) as MCBB_WooCommerce::process in mcp-billing-bridge 0.3.1.
type accountRequest struct {
	ExternalCustomerID string `json:"external_customer_id"`
	OrderRef           string `json:"order_ref"`
	PlanCode           string `json:"plan_code"`
	PeriodEnd          string `json:"period_end"` // gmdate('Y-m-d\TH:i:s\Z')
}

// lastAttempt reports whether the running job is on its final allowed
// attempt (the worker passes attempt/max in ctx). Direct calls outside a
// job are never final.
func lastAttempt(ctx context.Context) bool {
	attempt, max := jobs.AttemptFromContext(ctx)
	return max > 0 && attempt >= max
}

// permanent reports a failure retrying cannot fix: a non-retryable billing
// server answer (4xx other than 408/429, or a contract violation). Local
// errors (database, secret decryption, configuration) are retried.
func permanent(err error) bool {
	var me *MCBBError
	return errors.As(err, &me) && !me.Retryable()
}

func createKey(orderID string, retry int) string {
	if retry > 0 {
		return fmt.Sprintf("create:%s:retry%d", orderID, retry)
	}
	return "create:" + orderID
}

// provision is the commerce.provision job: create the order's entitlement
// (once, UNIQUE order_id) and its create op (once per idempotency key),
// call POST /accounts, then activate and email the customer a magic link.
// Permanent billing-server failures fail the order at once
// (commerce.entitlement.failed); every other failure returns the error so
// the job retries (8 attempts), and on the last attempt the order fails
// (commerce.order.provision_failed).
func (s *service) provision(ctx context.Context, raw json.RawMessage) error {
	var p provisionPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.OrderID == "" {
		return nil // malformed payload: nothing to retry
	}
	final := lastAttempt(ctx)
	o, err := s.Order(ctx, p.OrderID)
	if errors.Is(err, ErrNotFound) {
		s.log.Warn("commerce: provision unknown order", "order", p.OrderID)
		return nil
	}
	if err != nil {
		return s.retryOrFail(ctx, p, nil, "", err, final)
	}
	if o.snapshot().Kind != "" && o.snapshot().Kind != ProductProvisioning {
		return nil
	}
	if o.Status != OrderPaid && o.Status != OrderProvisioning {
		return nil // pending, refunded, cancelled, already active or failed
	}
	key := createKey(o.ID, p.Retry)
	ent, body, err := s.prepareProvision(ctx, o)
	if err != nil {
		return s.retryOrFail(ctx, p, o, "", err, final)
	}
	res, err := s.callOp(ctx, ent.ID, "create", key, "POST", "/accounts", body, false, final)
	if err != nil {
		if permanent(err) {
			return s.failProvision(ctx, o, ent.ID, key, "commerce.entitlement.failed", err)
		}
		return s.retryOrFail(ctx, p, o, ent.ID, err, final)
	}
	var acct accountResult
	_ = json.Unmarshal(res.Data, &acct)
	if acct.Status != "active" && acct.Status != "pending" {
		return s.failProvision(ctx, o, ent.ID, key, "commerce.entitlement.failed", &MCBBError{Status: 200, Message: "response did not match the endpoint contract"})
	}
	end, ok := parsePeriodEnd(acct.PeriodEnd)
	if !ok { // GoRun's choice (not PHP behaviour): keep the period_end we sent
		end, _ = parsePeriodEnd(body.PeriodEnd)
		s.log.Warn("commerce: unparseable period_end from billing server; using the requested one", "order", o.ID, "period_end", acct.PeriodEnd)
	}
	return s.activate(ctx, o, ent, end, res.Data)
}

// retryOrFail returns err for a job retry, or on the last attempt fails the
// order (if known) with commerce.order.provision_failed.
func (s *service) retryOrFail(ctx context.Context, p provisionPayload, o *Order, entID string, err error, final bool) error {
	if !final || o == nil {
		return err
	}
	return s.failProvision(ctx, o, entID, createKey(o.ID, p.Retry), "commerce.order.provision_failed", err)
}

// prepareProvision creates (or loads) the entitlement and returns the
// request body: starts_at is the later of now and the latest ends_at of the
// customer's pending or active entitlements on the same server (buying
// again extends, even while the earlier one is still being provisioned),
// ends_at that plus the product's months.
func (s *service) prepareProvision(ctx context.Context, o *Order) (*Entitlement, accountRequest, error) {
	snap := o.snapshot()
	var entID, endsAt string
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		start, end, err := s.periodWindow(ctx, tx, o)
		if err != nil {
			return err
		}
		ts := s.nowString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO entitlements (id, customer_id, order_id, product_id, server_id, plan_code, external_id, status,
			starts_at, ends_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?) ON CONFLICT (order_id) DO NOTHING`,
			db.NewID(), o.CustomerID, o.ID, snap.ID, snap.ServerID, snap.PlanCode, o.CustomerID,
			db.FormatTime(start), db.FormatTime(end), ts, ts); err != nil {
			return fmt.Errorf("commerce: create entitlement: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT id, ends_at FROM entitlements WHERE order_id = ?`, o.ID).Scan(&entID, &endsAt); err != nil {
			return fmt.Errorf("commerce: load entitlement: %w", err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE orders SET status = 'provisioning', updated_at = ? WHERE id = ? AND status = 'paid'`, ts, o.ID)
		return err
	})
	if err != nil {
		return nil, accountRequest{}, err
	}
	ent, err := s.Entitlement(ctx, entID)
	if err != nil {
		return nil, accountRequest{}, err
	}
	end, _ := db.ParseTime(endsAt)
	return ent, accountRequest{ExternalCustomerID: o.CustomerID, OrderRef: "gp_" + o.ID, PlanCode: snap.PlanCode,
		PeriodEnd: end.UTC().Format("2006-01-02T15:04:05Z")}, nil
}

// periodWindow is the access window of order o's entitlement: it starts now,
// or when the customer's latest other pending/active period on the same
// server ends if that is later, and lasts the product's access months.
func (s *service) periodWindow(ctx context.Context, tx *sql.Tx, o *Order) (start, end time.Time, err error) {
	snap := o.snapshot()
	start = s.now().UTC()
	var latest sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MAX(ends_at) FROM entitlements WHERE customer_id = ? AND server_id = ? AND status IN ('pending', 'active')
		AND order_id != ?`, o.CustomerID, snap.ServerID, o.ID).Scan(&latest); err != nil {
		return start, end, fmt.Errorf("commerce: latest period: %w", err)
	}
	if t, err := db.ParseTime(latest.String); latest.Valid && err == nil && t.After(start) {
		start = t
	}
	return start, addMonths(start, max(snap.AccessMonths, 1)), nil
}

// failProvision marks op, entitlement and order failed and audits action.
func (s *service) failProvision(ctx context.Context, o *Order, entID, opKey, action string, cause error) error {
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.nowString()
		if entID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'failed', updated_at = ? WHERE id = ? AND status = 'pending'`, now, entID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'failed', updated_at = ? WHERE id = ? AND status IN ('paid', 'provisioning')`, now, o.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provisioning_ops SET status = 'failed', last_error = CASE WHEN last_error = '' THEN ? ELSE last_error END,
			updated_at = ? WHERE idempotency_key = ? AND status != 'done'`, truncate(cause.Error(), maxErrorLen), now, opKey); err != nil {
			return err
		}
		target, id := "order", o.ID
		if entID != "" && action == "commerce.entitlement.failed" {
			target, id = "entitlement", entID
		}
		s.audit.RecordTx(ctx, tx, action, target, id, map[string]any{"order": o.ID, "entitlement": entID, "error": truncate(cause.Error(), maxErrorLen)})
		return nil
	})
	if err != nil {
		return fmt.Errorf("commerce: mark provisioning failed: %w", err)
	}
	s.log.Error("commerce: provisioning failed", "order", o.ID, "action", action, "err", cause)
	return nil
}

// RetryProvisioning re-enqueues provisioning for a failed order with a
// fresh create-op key (":retryN"); the failed entitlement goes back to
// pending. Audited as commerce.order.retry.
func (s *service) RetryProvisioning(ctx context.Context, orderID string) error {
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		o, err := getOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.Status != OrderFailed || o.PaidAt == "" || (o.snapshot().Kind != "" && o.snapshot().Kind != ProductProvisioning) {
			return &ValidationError{Fields: map[string]string{"order": "commerce.error.not_retryable"}}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE kind = ? AND idempotency_key LIKE ?`, JobProvision,
			JobProvision+":"+orderID+":retry%").Scan(&n); err != nil {
			return err
		}
		retry := n + 1
		now := s.nowString()
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'paid', updated_at = ? WHERE id = ?`, now, orderID); err != nil {
			return err
		}
		// The customer paid long ago perhaps, but gets the full window from
		// the retry: recompute it from now (and any later period).
		start, end, err := s.periodWindow(ctx, tx, o)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'pending', starts_at = ?, ends_at = ?, updated_at = ? WHERE order_id = ? AND status = 'failed'`,
			db.FormatTime(start), db.FormatTime(end), now, orderID); err != nil {
			return err
		}
		key := fmt.Sprintf("%s:%s:retry%d", JobProvision, orderID, retry)
		if _, err := s.d.Jobs.EnqueueTx(ctx, tx, JobProvision, provisionPayload{OrderID: orderID, Retry: retry, JobKey: key}, jobs.Idempotency(key)); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "commerce.order.retry", "order", orderID, map[string]any{"retry": retry})
		return nil
	})
}

// activate stores the result, activates entitlement and order, then (after
// commit) emails the customer a magic link and emits the hook. A refund
// that landed meanwhile suspends the fresh account instead.
func (s *service) activate(ctx context.Context, o *Order, ent *Entitlement, end time.Time, data json.RawMessage) error {
	ent.EndsAt = db.FormatTime(end)
	refunded := false
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.nowString()
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM orders WHERE id = ?`, o.ID).Scan(&status); err != nil {
			return err
		}
		refunded = status == OrderRefunded
		if refunded {
			if _, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'suspended', ends_at = ?, last_sync_at = ?, meta_json = ?, updated_at = ?
				WHERE id = ?`, db.FormatTime(end), now, string(data), now, ent.ID); err != nil {
				return err
			}
			// A refund worker may have suspended before the in-flight create
			// completed. Use a fresh operation after creation to close that race.
			return s.enqueueSuspendTx(ctx, tx, ent.ID, "refund_after_provision")
		}
		entStatus := EntitlementActive
		res, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = ?, ends_at = ?, last_sync_at = ?, meta_json = ?, updated_at = ?
			WHERE id = ? AND status = 'pending'`, entStatus, db.FormatTime(end), now, string(data), now, ent.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errAlreadyDone
		}
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'active', updated_at = ? WHERE id = ? AND status IN ('paid', 'provisioning')`, now, o.ID); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "commerce.entitlement.activated", "entitlement", ent.ID, map[string]any{"order": o.ID, "ends_at": db.FormatTime(end)})
		return nil
	})
	if errors.Is(err, errAlreadyDone) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commerce: activate: %w", err)
	}
	if refunded {
		return nil
	}
	s.emailAccess(ctx, o, ent)
	s.d.Hooks.Emit(ctx, HookEntitlementActivate, activatedEvent{EntitlementID: ent.ID, OrderID: o.ID, CustomerID: o.CustomerID,
		PlanCode: ent.PlanCode, ServerID: ent.ServerID, EndsAt: db.FormatTime(end)})
	return nil
}

var errAlreadyDone = errors.New("commerce: already activated")

// emailAccess sends the "your access is ready" email with a magic link to
// /account. Failures are logged: access is active either way and the
// customer can request a new link from /account.
func (s *service) emailAccess(ctx context.Context, o *Order, ent *Entitlement) {
	userID, email, locale, role, err := s.customerContact(ctx, o.CustomerID)
	if err != nil || userID == "" {
		s.log.Warn("commerce: no user to email", "order", o.ID, "err", err)
		return
	}
	if locale == "" {
		locale = s.siteLocale(ctx)
	}
	// Magic links only for customer accounts: a staff member who buys with
	// their work address gets a plain link to /account (they sign in normally).
	link := s.baseURL(ctx) + "/account"
	if role == auth.RoleCustomer {
		token, err := auth.NewMagicLink(ctx, s.d, userID, customerLinkTTL)
		if err != nil {
			s.log.Error("commerce: magic link", "order", o.ID, "err", err)
			return
		}
		link = s.baseURL(ctx) + "/auth/magic/" + token
	}
	subject, html, text := accessEmail(s.d.I18n, locale, accessMail{Link: link, Product: o.ProductName, Order: o.Number, EndsAt: dateOnly(ent.EndsAt)})
	if err := s.sendMail(ctx, email, subject, html, text); err != nil {
		s.log.Error("commerce: send access email", "order", o.ID, "err", err)
	}
}

// suspendJob is the commerce.suspend job: suspend the account on the
// billing server unless the customer still holds another active
// entitlement on that server (the account is shared per customer).
func (s *service) suspendJob(ctx context.Context, raw json.RawMessage) error {
	var p suspendPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.EntitlementID == "" {
		return nil
	}
	e, err := s.Entitlement(ctx, p.EntitlementID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.Status != EntitlementSuspended && e.Status != EntitlementExpired {
		return nil // resumed or reactivated meanwhile
	}
	shared, err := s.sharedAccountActive(ctx, e)
	if err != nil {
		return err
	}
	if shared {
		s.log.Info("commerce: account kept active by another entitlement", "entitlement", e.ID)
		return nil
	}
	key := p.Key
	if key == "" {
		key = p.Reason + ":" + e.ID
	}
	final := lastAttempt(ctx)
	res, err := s.callOp(ctx, e.ID, "suspend", key, "POST", accountPath(e.ExternalID, "suspend"),
		map[string]string{"reason": "GoRun " + p.Reason + " (order " + e.OrderNumber + ")"}, false, final)
	if err == nil {
		err = expectStatus(res.Data, "suspended", "inactive")
	}
	if err != nil {
		if !permanent(err) && !final {
			return err
		}
		s.audit.Record(ctx, "commerce.entitlement.suspend_failed", "entitlement", e.ID, map[string]any{"reason": p.Reason, "error": truncate(err.Error(), maxErrorLen)})
		return nil
	}
	s.audit.Record(ctx, "commerce.entitlement.suspended_remote", "entitlement", e.ID, map[string]any{"reason": p.Reason})
	return nil
}

// resumeJob calls resume on the billing server after a subscription becomes
// payable again. Local status is already active.
func (s *service) resumeJob(ctx context.Context, raw json.RawMessage) error {
	var p suspendPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.EntitlementID == "" {
		return nil
	}
	e, err := s.Entitlement(ctx, p.EntitlementID)
	if errors.Is(err, ErrNotFound) || e == nil || e.ExternalID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	key := p.Key
	if key == "" {
		key = "billing:" + e.ID
	}
	final := lastAttempt(ctx)
	res, err := s.callOp(ctx, e.ID, "resume", key, "POST", accountPath(e.ExternalID, "resume"), map[string]string{}, false, final)
	if err == nil {
		err = expectStatus(res.Data, "active")
	}
	if err != nil {
		if !permanent(err) && !final {
			return err
		}
		s.audit.Record(ctx, "commerce.entitlement.resume_failed", "entitlement", e.ID, map[string]any{"error": truncate(err.Error(), maxErrorLen)})
		return nil
	}
	s.audit.Record(ctx, "commerce.entitlement.resume", "entitlement", e.ID, map[string]any{"reason": p.Reason})
	return nil
}

// sharedAccountActive reports whether the customer holds another active,
// unexpired entitlement on e's server: the billing account is shared per
// customer, so it must not be suspended remotely.
func (s *service) sharedAccountActive(ctx context.Context, e *Entitlement) (bool, error) {
	var others int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entitlements WHERE customer_id = ? AND server_id = ? AND id != ? AND status = 'active' AND ends_at > ?`,
		e.CustomerID, e.ServerID, e.ID, s.nowString()).Scan(&others)
	return others > 0, err
}

// expectStatus validates a suspend/resume answer like PHP valid_result:
// data.status must be one of want.
func expectStatus(data json.RawMessage, want ...string) error {
	var r struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(data, &r)
	for _, w := range want {
		if r.Status == w {
			return nil
		}
	}
	return &MCBBError{Status: 200, Message: fmt.Sprintf("response status %q did not match the endpoint contract", r.Status)}
}

// expire is the daily commerce.expire cron: entitlements past ends_at
// become expired and a commerce.suspend job is enqueued for each.
func (s *service) expire(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM entitlements WHERE status = 'active' AND ends_at != '' AND ends_at <= ?`, s.nowString())
	if err != nil {
		return fmt.Errorf("commerce: expire scan: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE entitlements SET status = 'expired', updated_at = ? WHERE id = ? AND status = 'active'`, s.nowString(), id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			s.audit.RecordTx(ctx, tx, "commerce.entitlement.expire", "entitlement", id, nil)
			return s.enqueueSuspendTx(ctx, tx, id, "expire")
		})
		if err != nil {
			return fmt.Errorf("commerce: expire %s: %w", id, err)
		}
	}
	return nil
}

// dateOnly formats a stored timestamp as YYYY-MM-DD.
func dateOnly(ts string) string {
	if t, err := db.ParseTime(ts); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return ts
}
