package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const entitlementSelect = `SELECT e.id, e.customer_id, COALESCE(o.email, ''), e.order_id, COALESCE(o.number, ''), e.product_id,
	COALESCE(json_extract(o.product_snapshot_json, '$.name'), ''), e.server_id, COALESCE(b.name, ''), e.plan_code, e.external_id, e.status,
	e.starts_at, e.ends_at, e.last_sync_at, e.meta_json, e.created_at
	FROM entitlements e LEFT JOIN orders o ON o.id = e.order_id LEFT JOIN billing_servers b ON b.id = e.server_id`

func scanEntitlement(row interface{ Scan(...any) error }) (*Entitlement, error) {
	var e Entitlement
	var meta string
	if err := row.Scan(&e.ID, &e.CustomerID, &e.Email, &e.OrderID, &e.OrderNumber, &e.ProductID, &e.ProductName, &e.ServerID, &e.ServerName,
		&e.PlanCode, &e.ExternalID, &e.Status, &e.StartsAt, &e.EndsAt, &e.LastSyncAt, &meta, &e.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("commerce: scan entitlement: %w", err)
	}
	_ = json.Unmarshal([]byte(meta), &e.Meta)
	if e.Meta == nil {
		e.Meta = map[string]any{}
	}
	return &e, nil
}

// Entitlement returns one entitlement.
func (s *service) Entitlement(ctx context.Context, id string) (*Entitlement, error) {
	return scanEntitlement(s.db.QueryRowContext(ctx, entitlementSelect+` WHERE e.id = ?`, id))
}

func (s *service) Entitlements(ctx context.Context, q ListQuery) ([]Entitlement, int, error) {
	page, perPage := pageBounds(q.Page, q.PerPage)
	var where []string
	var args []any
	if q.Status != "" {
		where, args = append(where, "e.status = ?"), append(args, q.Status)
	}
	if q.CustomerID != "" {
		where, args = append(where, "e.customer_id = ?"), append(args, q.CustomerID)
	}
	if q.Search != "" {
		like := likePattern(strings.TrimSpace(q.Search))
		where, args = append(where, `(o.email LIKE ? ESCAPE '\' OR o.number LIKE ? ESCAPE '\' OR e.plan_code LIKE ? ESCAPE '\')`), append(args, like, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entitlements e LEFT JOIN orders o ON o.id = e.order_id`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("commerce: count entitlements: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, entitlementSelect+cond+` ORDER BY e.created_at DESC, e.id DESC LIMIT ? OFFSET ?`, append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("commerce: list entitlements: %w", err)
	}
	defer rows.Close()
	out := []Entitlement{}
	for rows.Next() {
		e, err := scanEntitlement(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

func (s *service) CustomerEntitlements(ctx context.Context, userID string) ([]Entitlement, error) {
	cid, err := s.customerIDForUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return []Entitlement{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, _, err := s.Entitlements(ctx, ListQuery{CustomerID: cid, PerPage: 100})
	return out, err
}

// opResult is the outcome of callOp.
type opResult struct {
	Data     json.RawMessage
	Attempts int
}

// callOp performs one provisioning operation exactly once per idempotency
// key: the op row is created on first use (request body frozen), a done op
// returns its stored response without calling the server, and every
// attempt is counted. A permanent failure (or the last allowed attempt)
// marks the op failed. redact drops the response body (login links are
// credentials). lastAttempt marks the op failed on a retryable failure too.
func (s *service) callOp(ctx context.Context, entID, kind, key, method, path string, body any, redact, lastAttempt bool) (opResult, error) {
	reqBody, err := encodeMCBBBody(body)
	if err != nil {
		return opResult{}, err
	}
	req := "{}"
	if len(reqBody) > 0 {
		req = string(reqBody)
	}
	now := s.nowString()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO provisioning_ops (id, entitlement_id, kind, idempotency_key, request_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?) ON CONFLICT (idempotency_key) DO NOTHING`, db.NewID(), entID, kind, key, req, now, now); err != nil {
		return opResult{}, fmt.Errorf("commerce: create op: %w", err)
	}
	var opID, status, storedReq, resp, lastErr string
	var attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT id, status, request_json, response_json, attempts, last_error FROM provisioning_ops WHERE idempotency_key = ?`, key).
		Scan(&opID, &status, &storedReq, &resp, &attempts, &lastErr); err != nil {
		return opResult{}, fmt.Errorf("commerce: load op: %w", err)
	}
	switch status {
	case "done":
		return opResult{Data: json.RawMessage(resp), Attempts: attempts}, nil
	case "failed":
		return opResult{Attempts: attempts}, &MCBBError{Status: http4xx, Message: "operation failed permanently: " + lastErr}
	}
	// Atomically claim the pending row so concurrent callers cannot both
	// execute. Correctness must not depend solely on the remote Idempotency-Key.
	claim, err := s.db.ExecContext(ctx, `UPDATE provisioning_ops SET attempts = attempts + 1, updated_at = ?
		WHERE id = ? AND status = 'pending' AND attempts = ?`, now, opID, attempts)
	if err != nil {
		return opResult{}, fmt.Errorf("commerce: claim op: %w", err)
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		if err := s.db.QueryRowContext(ctx, `SELECT status, response_json, attempts, last_error FROM provisioning_ops WHERE id = ?`, opID).
			Scan(&status, &resp, &attempts, &lastErr); err != nil {
			return opResult{}, fmt.Errorf("commerce: reload op: %w", err)
		}
		switch status {
		case "done":
			return opResult{Data: json.RawMessage(resp), Attempts: attempts}, nil
		case "failed":
			return opResult{Attempts: attempts}, &MCBBError{Status: http4xx, Message: "operation failed permanently: " + lastErr}
		}
		return opResult{Attempts: attempts}, &MCBBError{Status: 0, Message: "operation in flight"}
	}
	attempts++
	r, err := s.serverClientForEntitlement(ctx, entID)
	if err != nil { // local failure (config, secret, database): retried by the caller
		s.finishOp(ctx, opID, attempts, "", err, lastAttempt)
		return opResult{Attempts: attempts}, err
	}
	var send any
	if method != "GET" {
		send = json.RawMessage(storedReq)
	}
	data, callErr := r.Do(ctx, method, path, send, key)
	if callErr != nil {
		final := !retryable(callErr) || lastAttempt
		s.finishOp(ctx, opID, attempts, "", callErr, final)
		return opResult{Attempts: attempts}, callErr
	}
	stored := string(data)
	if redact {
		stored = `{"redacted":true}`
	}
	s.finishOp(ctx, opID, attempts, stored, nil, true)
	return opResult{Data: data, Attempts: attempts}, nil
}

// http4xx marks a permanently failed op replay (not retryable).
const http4xx = 400

func (s *service) finishOp(ctx context.Context, opID string, attempts int, resp string, callErr error, final bool) {
	status, lastErr := "pending", ""
	switch {
	case callErr == nil:
		status = "done"
	case final:
		status, lastErr = "failed", truncate(callErr.Error(), maxErrorLen)
	default:
		lastErr = truncate(callErr.Error(), maxErrorLen)
	}
	if resp == "" {
		resp = "{}"
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE provisioning_ops SET status = ?, attempts = ?, last_error = ?, response_json = ?, updated_at = ? WHERE id = ?`,
		status, attempts, lastErr, resp, s.nowString(), opID); err != nil {
		s.log.Error("commerce: update op", "op", opID, "err", err)
	}
}

func (s *service) serverClientForEntitlement(ctx context.Context, entID string) (*mcbbClient, error) {
	var serverID string
	if err := s.db.QueryRowContext(ctx, `SELECT server_id FROM entitlements WHERE id = ?`, entID).Scan(&serverID); err != nil {
		return nil, fmt.Errorf("commerce: entitlement server: %w", err)
	}
	c, err := s.clientFor(ctx, serverID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNoServer
	}
	return c, err
}

// accountPath is /accounts/{external_id}[/suffix].
func accountPath(externalID, suffix string) string {
	p := "/accounts/" + url.PathEscape(externalID)
	if suffix != "" {
		p += "/" + suffix
	}
	return p
}

func (s *service) setEntitlementStatus(ctx context.Context, id, status string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE entitlements SET status = ?, last_sync_at = ?, updated_at = ? WHERE id = ?`,
		status, s.nowString(), s.nowString(), id); err != nil {
		return fmt.Errorf("commerce: update entitlement: %w", err)
	}
	return nil
}

// Suspend suspends the entitlement, and the account on the billing server
// unless the customer holds another active entitlement there (the account
// is shared per customer).
func (s *service) Suspend(ctx context.Context, id string) error {
	e, err := s.Entitlement(ctx, id)
	if err != nil {
		return err
	}
	shared, err := s.sharedAccountActive(ctx, e)
	if err != nil {
		return err
	}
	if !shared {
		res, err := s.callOp(ctx, e.ID, "suspend", "suspend:"+e.ID+":"+db.NewID(), "POST", accountPath(e.ExternalID, "suspend"),
			map[string]string{"reason": "Suspended by an administrator"}, false, true)
		if err == nil {
			err = expectStatus(res.Data, "suspended", "inactive")
		}
		if err != nil {
			return err
		}
	}
	if err := s.setEntitlementStatus(ctx, e.ID, EntitlementSuspended); err != nil {
		return err
	}
	s.audit.Record(ctx, "commerce.entitlement.suspend", "entitlement", e.ID, map[string]any{"reason": "admin", "remote_kept_active": shared})
	return nil
}

// Resume resumes the account on the billing server, then the entitlement.
// An entitlement whose period has ended cannot be resumed (the customer
// buys again to extend).
func (s *service) Resume(ctx context.Context, id string) error {
	e, err := s.Entitlement(ctx, id)
	if err != nil {
		return err
	}
	if end, err := db.ParseTime(e.EndsAt); err == nil && !end.After(s.now()) {
		return ErrPeriodEnded
	}
	res, err := s.callOp(ctx, e.ID, "resume", "resume:"+e.ID+":"+db.NewID(), "POST", accountPath(e.ExternalID, "resume"), map[string]string{}, false, true)
	if err == nil {
		err = expectStatus(res.Data, "active")
	}
	if err != nil {
		return err
	}
	if err := s.setEntitlementStatus(ctx, e.ID, EntitlementActive); err != nil {
		return err
	}
	s.audit.Record(ctx, "commerce.entitlement.resume", "entitlement", e.ID, nil)
	return nil
}

// Reconcile reads GET /accounts/{id} and adopts the server's status and
// period end.
func (s *service) Reconcile(ctx context.Context, id string) error {
	e, err := s.Entitlement(ctx, id)
	if err != nil {
		return err
	}
	res, err := s.callOp(ctx, e.ID, "reconcile", "reconcile:"+e.ID+":"+db.NewID(), "GET", accountPath(e.ExternalID, ""), nil, false, true)
	if err != nil {
		return err
	}
	var acct struct {
		Status    string `json:"status"`
		PeriodEnd any    `json:"period_end"`
	}
	_ = json.Unmarshal(res.Data, &acct)
	status := e.Status
	switch acct.Status {
	case "active", "pending":
		status = EntitlementActive
	case "suspended", "inactive":
		status = EntitlementSuspended
	case "expired":
		status = EntitlementExpired
	}
	ends := e.EndsAt
	if t, ok := parsePeriodEnd(acct.PeriodEnd); ok {
		ends = db.FormatTime(t)
	}
	now := s.nowString()
	if _, err := s.db.ExecContext(ctx, `UPDATE entitlements SET status = ?, ends_at = ?, last_sync_at = ?, meta_json = ?, updated_at = ? WHERE id = ?`,
		status, ends, now, string(res.Data), now, e.ID); err != nil {
		return fmt.Errorf("commerce: reconcile: %w", err)
	}
	s.audit.Record(ctx, "commerce.entitlement.reconcile", "entitlement", e.ID, map[string]any{"from": e.Status, "to": status, "ends_at": ends})
	return nil
}

// LoginLink asks the billing server for a short-lived login URL for an
// active entitlement. Only absolute https URLs are returned (http too when
// AllowPrivateNet).
func (s *service) LoginLink(ctx context.Context, id string) (string, error) {
	e, err := s.Entitlement(ctx, id)
	if err != nil {
		return "", err
	}
	if e.Status != EntitlementActive {
		return "", ErrNotActive
	}
	if end, err := db.ParseTime(e.EndsAt); err == nil && !end.After(s.now()) {
		return "", ErrNotActive // past its period, waiting for the expiry cron
	}
	res, err := s.callOp(ctx, e.ID, "login_link", "login:"+e.ID+":"+db.NewID(), "POST", accountPath(e.ExternalID, "login-link"), map[string]string{}, true, true)
	if err != nil {
		return "", err
	}
	var out struct {
		URL      string `json:"url"`
		LoginURL string `json:"login_url"`
		Link     string `json:"link"`
	}
	_ = json.Unmarshal(res.Data, &out)
	link := firstNonEmpty(out.URL, out.LoginURL, out.Link)
	u, perr := url.Parse(link)
	if perr != nil || u.Host == "" || !(u.Scheme == "https" || (u.Scheme == "http" && s.d.Config.AllowPrivateNet)) {
		return "", &MCBBError{Status: 200, Message: "login link is not an absolute https URL"}
	}
	s.audit.Record(ctx, "commerce.entitlement.login_link", "entitlement", e.ID, map[string]any{"host": u.Host})
	return u.String(), nil
}

// periodLayouts are the period_end string formats accepted from billing
// servers.
var periodLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05Z0700", "2006-01-02 15:04:05", "2006-01-02"}

// Epoch bounds for a numeric period_end: at least 9 digits (1973-03-03)
// and before year 10000. Smaller numbers ("12", 3600) are durations or
// junk, not timestamps, and fall back like any unparseable value.
const (
	minEpochPeriodEnd = 100_000_000
	maxEpochPeriodEnd = 253_402_300_799 // 9999-12-31T23:59:59Z
)

func epochPeriodEnd(n int64) (time.Time, bool) {
	if n < minEpochPeriodEnd || n > maxEpochPeriodEnd {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// parsePeriodEnd accepts RFC 3339 (with or without a colon in the offset),
// "Y-m-d H:i:s", "Y-m-d\TH:i:s", "Y-m-d" (UTC) and numeric epoch seconds
// within the epoch bounds (as a JSON number or a string of digits).
func parsePeriodEnd(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		if x != x || x > maxEpochPeriodEnd { // NaN, or beyond int64-safe range
			return time.Time{}, false
		}
		return epochPeriodEnd(int64(x))
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return epochPeriodEnd(n)
		}
		return time.Time{}, false
	case string:
		str := strings.TrimSpace(x)
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return epochPeriodEnd(n)
		}
		for _, layout := range periodLayouts {
			if t, err := time.Parse(layout, str); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// addMonths adds months to t, clamping the day to the target month's last
// day (Jan 31 + 1 month = Feb 28/29), like MCBB_WooCommerce::add_months.
func addMonths(t time.Time, months int) time.Time {
	t = t.UTC()
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC).AddDate(0, months, 0)
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(first.Year(), first.Month(), min(t.Day(), last), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}
