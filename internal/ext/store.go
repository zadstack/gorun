package ext

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Extension statuses (spec §3).
const (
	StatusInstalled = "installed"
	StatusActive    = "active"
	StatusDisabled  = "disabled"
	StatusError     = "error"
)

// Extension is an installed extension. The HMAC secret and API key
// plaintext are never part of it (they are shown once, at install).
type Extension struct {
	Slug          string            `json:"slug"`
	Name          map[string]string `json:"name"`
	Version       string            `json:"version"`
	Status        string            `json:"status"`
	EndpointURL   string            `json:"endpoint_url"`
	APIKeyID      string            `json:"api_key_id"`
	Failures      int               `json:"failures"`
	TotalFailures int               `json:"total_failures"`
	InstalledAt   string            `json:"installed_at"`
	ActivatedAt   string            `json:"activated_at"`
	LastError     string            `json:"last_error"`
	Manifest      Manifest          `json:"manifest"`
	secretEnc     string
}

// HooksCount is the number of hook subscriptions declared.
func (x Extension) HooksCount() int { return len(x.Manifest.Hooks) }

// DisplayName returns the extension name in locale.
func (x Extension) DisplayName(locale string) string { return x.Manifest.DisplayName(locale) }

// Delivery is one bridge attempt in the delivery log.
type Delivery struct {
	ID         string `json:"id"`
	DeliveryID string `json:"delivery_id"`
	Hook       string `json:"hook"`
	Kind       string `json:"kind"`
	Status     string `json:"status"` // ok | error | timeout
	HTTPStatus int    `json:"http_status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
	CreatedAt  string `json:"created_at"`
}

// InstallResult is returned once by Install: APIKey (empty when the
// manifest requests no permissions) and Secret are not stored in plaintext
// and cannot be shown again.
type InstallResult struct {
	Extension *Extension `json:"extension"`
	APIKey    string     `json:"api_key"`
	Secret    string     `json:"secret"`
}

const extColumns = `slug, version, manifest_json, status, endpoint_url, secret_enc, api_key_id, failures, total_failures,
	installed_at, activated_at, last_error`

func scanExt(row interface{ Scan(...any) error }) (*Extension, error) {
	var x Extension
	var raw string
	err := row.Scan(&x.Slug, &x.Version, &raw, &x.Status, &x.EndpointURL, &x.secretEnc, &x.APIKeyID, &x.Failures,
		&x.TotalFailures, &x.InstalledAt, &x.ActivatedAt, &x.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ext: scan: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &x.Manifest); err != nil {
		return nil, fmt.Errorf("ext: manifest of %s: %w", x.Slug, err)
	}
	x.Name = x.Manifest.Name
	return &x, nil
}

func (s *service) get(ctx context.Context, slug string) (*Extension, error) {
	return scanExt(s.db.QueryRowContext(ctx, `SELECT `+extColumns+` FROM extensions WHERE slug = ?`, slug))
}

func (s *service) list(ctx context.Context, where string, args ...any) ([]Extension, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+extColumns+` FROM extensions `+where+` ORDER BY slug`, args...)
	if err != nil {
		return nil, fmt.Errorf("ext: list: %w", err)
	}
	defer rows.Close()
	var out []Extension
	for rows.Next() {
		x, err := scanExt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

func (s *service) setStatus(ctx context.Context, slug, status string, extra string, args ...any) error {
	q := `UPDATE extensions SET status = ?` + extra + ` WHERE slug = ?`
	all := append(append([]any{status}, args...), slug)
	if _, err := s.db.ExecContext(ctx, q, all...); err != nil {
		return fmt.Errorf("ext: set status %s: %w", slug, err)
	}
	return nil
}

const keepDeliveries = 200

func (s *service) recordDelivery(ctx context.Context, slug string, d Delivery) {
	ctx = context.WithoutCancel(ctx)
	_, err := s.db.ExecContext(ctx, `INSERT INTO extension_deliveries (id, extension_slug, delivery_id, hook, kind, status,
		http_status, duration_ms, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, slug, d.DeliveryID, d.Hook, d.Kind, d.Status, d.HTTPStatus, d.DurationMS, d.Error, d.CreatedAt)
	if err != nil {
		s.log.Warn("ext: record delivery", "ext", slug, "err", err)
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM extension_deliveries WHERE extension_slug = ? AND id NOT IN
		(SELECT id FROM extension_deliveries WHERE extension_slug = ? ORDER BY id DESC LIMIT ?)`, slug, slug, keepDeliveries)
}

// Deliveries returns the newest limit (1..200, default 50) delivery
// attempts of slug, newest first.
func (s *service) Deliveries(ctx context.Context, slug string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > keepDeliveries {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, delivery_id, hook, kind, status, http_status, duration_ms, error, created_at
		FROM extension_deliveries WHERE extension_slug = ? ORDER BY id DESC LIMIT ?`, slug, limit)
	if err != nil {
		return nil, fmt.Errorf("ext: deliveries: %w", err)
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.DeliveryID, &d.Hook, &d.Kind, &d.Status, &d.HTTPStatus, &d.DurationMS, &d.Error, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("ext: scan delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
