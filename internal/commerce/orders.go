package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	orderColumns     = `id, number, customer_id, email, status, currency, total_minor, product_id, product_snapshot_json, stripe_session_id, stripe_payment_intent, paid_at, created_at, updated_at, request_status, fulfillment_note, COALESCE((SELECT status FROM commerce_request_receipts WHERE order_id=orders.id),''), COALESCE((SELECT last_error FROM commerce_request_receipts WHERE order_id=orders.id),''), COALESCE((SELECT sent_at FROM commerce_request_receipts WHERE order_id=orders.id),'')`
	firstOrderNumber = 1000
)

// snapshot is the product as sold, frozen on the order.
type snapshot struct {
	Intake        map[string]string `json:"intake,omitempty"`
	IntakeSchema  []IntakeField     `json:"intake_schema,omitempty"`
	Kind          string            `json:"kind"`
	DigitalFile   string            `json:"digital_file,omitempty"`
	DownloadDays  int               `json:"download_days"`
	ID            string            `json:"id"`
	Slug          string            `json:"slug"`
	Name          string            `json:"name"`
	PlanCode      string            `json:"plan_code"`
	ServerID      string            `json:"server_id"`
	AccessMonths  int               `json:"access_months"`
	PriceMinor    int64             `json:"price_minor"`
	Currency      string            `json:"currency"`
	BillingMode   string            `json:"billing_mode,omitempty"`
	Interval      string            `json:"interval,omitempty"`
	StripePriceID string            `json:"stripe_price_id,omitempty"`
	TrialDays     int               `json:"trial_days,omitempty"`
}

func snapshotOf(p *Product) snapshot {
	return snapshot{Kind: p.Kind, DigitalFile: p.DigitalFile, DownloadDays: p.DownloadDays, ID: p.ID, Slug: p.Slug, Name: p.Name, PlanCode: p.PlanCode, ServerID: p.ServerID,
		AccessMonths: p.AccessMonths, PriceMinor: p.PriceMinor, Currency: p.Currency, BillingMode: p.BillingMode, Interval: p.Interval, StripePriceID: p.StripePriceID, TrialDays: p.TrialDays}
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanOrder(row interface{ Scan(...any) error }) (*Order, error) {
	var o Order
	var snap string
	if err := row.Scan(&o.ID, &o.Number, &o.CustomerID, &o.Email, &o.Status, &o.Currency, &o.TotalMinor, &o.ProductID, &snap,
		&o.StripeSessionID, &o.StripePaymentIntent, &o.PaidAt, &o.CreatedAt, &o.UpdatedAt, &o.RequestStatus, &o.FulfillmentNote, &o.ReceiptStatus, &o.ReceiptError, &o.ReceiptSentAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("commerce: scan order: %w", err)
	}
	_ = json.Unmarshal([]byte(snap), &o.ProductSnapshot)
	if o.ProductSnapshot == nil {
		o.ProductSnapshot = map[string]any{}
	}
	o.ProductName, _ = o.ProductSnapshot["name"].(string)
	return &o, nil
}

func (o *Order) snapshot() snapshot {
	var s snapshot
	b, _ := json.Marshal(o.ProductSnapshot)
	_ = json.Unmarshal(b, &s)
	return s
}

func getOrder(ctx context.Context, q queryer, id string) (*Order, error) {
	return scanOrder(q.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, id))
}

// Order returns one order.
func (s *service) Order(ctx context.Context, id string) (*Order, error) {
	return getOrder(ctx, s.db, id)
}

func (s *service) Orders(ctx context.Context, q ListQuery) ([]Order, int, error) {
	page, perPage := pageBounds(q.Page, q.PerPage)
	var where []string
	var args []any
	if q.Status != "" {
		where, args = append(where, "status = ?"), append(args, q.Status)
	}
	if q.CustomerID != "" {
		where, args = append(where, "customer_id = ?"), append(args, q.CustomerID)
	}
	if q.Search != "" {
		like := likePattern(strings.TrimSpace(q.Search))
		where, args = append(where, `(email LIKE ? ESCAPE '\' OR number LIKE ? ESCAPE '\')`), append(args, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM orders`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("commerce: count orders: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+orderColumns+` FROM orders`+cond+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("commerce: list orders: %w", err)
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *o)
	}
	return out, total, rows.Err()
}

// nextOrderNumber returns the next sequential order number; call it inside
// the transaction that inserts the order (BEGIN IMMEDIATE serialises it).
func nextOrderNumber(ctx context.Context, tx *sql.Tx) (string, error) {
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(CAST(number AS INTEGER)) FROM orders`).Scan(&last); err != nil {
		return "", fmt.Errorf("commerce: order number: %w", err)
	}
	n := int64(firstOrderNumber)
	if last.Valid && last.Int64 >= n {
		n = last.Int64 + 1
	}
	return strconv.FormatInt(n, 10), nil
}
