package webhooks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// Webhook is an endpoint subscribed to site events.
type Webhook struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	URL                 string   `json:"url" doc:"lists show origin and path only (query as ?…); get returns the full URL"`
	Events              []string `json:"events" doc:"event names, or [\"*\"] for every event"`
	Active              bool     `json:"active"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
	LastStatus          string   `json:"last_status" doc:"\"\" | delivered | retrying | failed"`
	LastDeliveryAt      string   `json:"last_delivery_at"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

// Delivery is one event sent (or being retried) to one webhook.
type Delivery struct {
	ID           string `json:"id"`
	WebhookID    string `json:"webhook_id"`
	Event        string `json:"event"`
	Status       string `json:"status" doc:"queued | delivered | retrying | failed"`
	Attempts     int    `json:"attempts"`
	ResponseCode int    `json:"response_code"`
	Error        string `json:"error"`
	DurationMS   int    `json:"duration_ms"`
	CreatedAt    string `json:"created_at"`
	DeliveredAt  string `json:"delivered_at"`
	Payload      string `json:"payload,omitempty" doc:"the signed JSON body; returned by the deliveries list"`
}

// Input is the writable state of a webhook (create, or update: nil fields keep).
type Input struct {
	Name   *string  `json:"name,omitempty"`
	URL    *string  `json:"url,omitempty"`
	Events []string `json:"events,omitempty"`
	Active *bool    `json:"active,omitempty"`
}

// ValidationError lists invalid fields as i18n keys.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "webhooks: invalid input" }

var (
	// ErrNotFound is returned for an unknown webhook or delivery.
	ErrNotFound = errors.New("webhooks: not found")
	// ErrInactive is returned when testing or redelivering to a disabled webhook.
	ErrInactive = errors.New("webhooks: webhook is disabled")
)

const (
	maxName        = 100
	maxURL         = 2048
	maxPayload     = 256 << 10
	deliverKind    = "webhooks.deliver"
	maxAttempts    = 6
	disableAfter   = 50 // consecutive failed deliveries before a webhook is disabled
	keepDeliveries = 30 * 24 * time.Hour
)

type service struct {
	db           *sql.DB
	jobs         *jobs.Queue
	audit        *audit.Writer
	log          *slog.Logger
	key          []byte // site secret key (AES-256) for webhook secrets
	allowPrivate bool   // dev / trusted internal endpoints: allow http and private addresses
	baseURL      func(ctx context.Context) string
	send         sender
	now          func() time.Time
}

func (s *service) record(ctx context.Context, action, id string, meta any) {
	if s.audit != nil {
		s.audit.Record(ctx, action, "webhook", id, meta)
	}
}

// randRead is crypto/rand.Read; tests replace it.
var randRead = rand.Read

// newSecret returns a fresh signing secret. It panics if the system's
// randomness fails rather than return a predictable secret.
func newSecret() string {
	b := make([]byte, 24)
	if _, err := randRead(b); err != nil {
		panic("webhooks: crypto/rand failed: " + err.Error())
	}
	return "whsec_" + hex.EncodeToString(b)
}

func aad(id string) string { return "webhook:" + id }

const hookColumns = `id, name, url, events_json, active, consecutive_failures, last_status, last_delivery_at, created_at, updated_at`

func scanHook(row interface{ Scan(...any) error }) (*Webhook, error) {
	var w Webhook
	var events string
	var active int
	if err := row.Scan(&w.ID, &w.Name, &w.URL, &events, &active, &w.ConsecutiveFailures, &w.LastStatus, &w.LastDeliveryAt, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("webhooks: scan: %w", err)
	}
	w.Active = active == 1
	if err := json.Unmarshal([]byte(events), &w.Events); err != nil || w.Events == nil {
		w.Events = []string{}
	}
	return &w, nil
}

// validate checks the merged state of a webhook.
func (s *service) validate(name, rawURL string, events []string) map[string]string {
	fields := map[string]string{}
	if n := strings.TrimSpace(name); n == "" || len([]rune(n)) > maxName {
		fields["name"] = "webhooks.error.name"
	}
	u, err := url.Parse(rawURL)
	switch {
	case err != nil || len(rawURL) > maxURL || u.Host == "" || u.User != nil || u.Fragment != "":
		fields["url"] = "webhooks.error.url"
	case u.Scheme != "https" && !(u.Scheme == "http" && s.allowPrivate):
		fields["url"] = "webhooks.error.https"
	}
	if len(events) == 0 {
		fields["events"] = "webhooks.error.events"
	}
	for _, e := range events {
		if e != "*" && !knownEvent(e) {
			fields["events"] = "webhooks.error.events"
		}
	}
	return fields
}

func cleanEvents(in []string) []string {
	out := []string{}
	for _, e := range in {
		if e = strings.TrimSpace(e); e != "" && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if slices.Contains(out, "*") {
		return []string{"*"}
	}
	slices.Sort(out)
	return out
}

func (s *service) List(ctx context.Context) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+hookColumns+` FROM webhooks ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("webhooks: list: %w", err)
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (s *service) Get(ctx context.Context, id string) (*Webhook, error) {
	return scanHook(s.db.QueryRowContext(ctx, `SELECT `+hookColumns+` FROM webhooks WHERE id = ?`, id))
}

// Create stores a webhook and returns its signing secret (shown once).
func (s *service) Create(ctx context.Context, in Input) (*Webhook, string, error) {
	name, rawURL := deref(in.Name), strings.TrimSpace(deref(in.URL))
	events := cleanEvents(in.Events)
	if f := s.validate(name, rawURL, events); len(f) > 0 {
		return nil, "", &ValidationError{Fields: f}
	}
	now := db.FormatTime(s.now())
	w := &Webhook{ID: db.NewID(), Name: strings.TrimSpace(name), URL: rawURL, Events: events, Active: in.Active == nil || *in.Active, CreatedAt: now, UpdatedAt: now}
	secret := newSecret()
	enc, err := db.Encrypt(s.key, []byte(secret), aad(w.ID))
	if err != nil {
		return nil, "", fmt.Errorf("webhooks: encrypt secret: %w", err)
	}
	ev, _ := json.Marshal(events)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO webhooks (id, name, url, events_json, secret_enc, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.URL, string(ev), enc, boolInt(w.Active), now, now); err != nil {
		return nil, "", fmt.Errorf("webhooks: create: %w", err)
	}
	s.record(ctx, "webhook.create", w.ID, map[string]any{"name": w.Name, "url": RedactURL(w.URL), "events": events})
	return w, secret, nil
}

// Update changes the given fields; enabling a webhook resets its failure count.
func (s *service) Update(ctx context.Context, id string, in Input) (*Webhook, error) {
	w, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		w.Name = strings.TrimSpace(*in.Name)
	}
	if in.URL != nil {
		w.URL = strings.TrimSpace(*in.URL)
	}
	if in.Events != nil {
		w.Events = cleanEvents(in.Events)
	}
	if f := s.validate(w.Name, w.URL, w.Events); len(f) > 0 {
		return nil, &ValidationError{Fields: f}
	}
	if in.Active != nil {
		if *in.Active && !w.Active {
			w.ConsecutiveFailures = 0
		}
		w.Active = *in.Active
	}
	w.UpdatedAt = db.FormatTime(s.now())
	ev, _ := json.Marshal(w.Events)
	if _, err := s.db.ExecContext(ctx, `UPDATE webhooks SET name = ?, url = ?, events_json = ?, active = ?, consecutive_failures = ?, updated_at = ? WHERE id = ?`,
		w.Name, w.URL, string(ev), boolInt(w.Active), w.ConsecutiveFailures, w.UpdatedAt, w.ID); err != nil {
		return nil, fmt.Errorf("webhooks: update: %w", err)
	}
	s.record(ctx, "webhook.update", w.ID, map[string]any{"url": RedactURL(w.URL), "events": w.Events, "active": w.Active})
	return w, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("webhooks: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.record(ctx, "webhook.delete", id, nil)
	return nil
}

// RotateSecret replaces the signing secret and returns the new one (shown once).
func (s *service) RotateSecret(ctx context.Context, id string) (string, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return "", err
	}
	secret := newSecret()
	enc, err := db.Encrypt(s.key, []byte(secret), aad(id))
	if err != nil {
		return "", fmt.Errorf("webhooks: encrypt secret: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE webhooks SET secret_enc = ?, updated_at = ? WHERE id = ?`, enc, db.FormatTime(s.now()), id); err != nil {
		return "", fmt.Errorf("webhooks: rotate secret: %w", err)
	}
	s.record(ctx, "webhook.secret_rotate", id, nil)
	return secret, nil
}

func (s *service) secret(ctx context.Context, id string) (string, error) {
	var enc string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_enc FROM webhooks WHERE id = ?`, id).Scan(&enc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	plain, err := db.Decrypt(s.key, enc, aad(id))
	return string(plain), err
}

const deliveryColumns = `id, webhook_id, event, status, attempts, response_code, error, duration_ms, created_at, delivered_at, payload`

func scanDelivery(row interface{ Scan(...any) error }) (*Delivery, error) {
	var d Delivery
	if err := row.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Status, &d.Attempts, &d.ResponseCode, &d.Error, &d.DurationMS, &d.CreatedAt, &d.DeliveredAt, &d.Payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("webhooks: scan delivery: %w", err)
	}
	return &d, nil
}

// Deliveries returns the newest deliveries of a webhook (limit 1..200).
func (s *service) Deliveries(ctx context.Context, webhookID string, limit int) ([]Delivery, error) {
	if _, err := s.Get(ctx, webhookID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries WHERE webhook_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, webhookID, limit)
	if err != nil {
		return nil, fmt.Errorf("webhooks: deliveries: %w", err)
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *service) delivery(ctx context.Context, id string) (*Delivery, error) {
	return scanDelivery(s.db.QueryRowContext(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries WHERE id = ?`, id))
}

// enqueue stores a delivery with payload and queues its job (in one tx).
func (s *service) enqueue(ctx context.Context, webhookID, event, payload string) (*Delivery, error) {
	var d *Delivery
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		d, err = s.enqueueTx(ctx, tx, webhookID, event, payload)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("webhooks: enqueue delivery: %w", err)
	}
	return d, nil
}

// enqueueTx stores a delivery and queues its job inside tx.
func (s *service) enqueueTx(ctx context.Context, tx *sql.Tx, webhookID, event, payload string) (*Delivery, error) {
	d := &Delivery{ID: db.NewID(), WebhookID: webhookID, Event: event, Status: "queued", CreatedAt: db.FormatTime(s.now()), Payload: payload}
	if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event, payload, status, created_at) VALUES (?, ?, ?, ?, 'queued', ?)`,
		d.ID, d.WebhookID, d.Event, d.Payload, d.CreatedAt); err != nil {
		return nil, err
	}
	if s.jobs != nil {
		if _, err := s.jobs.EnqueueTx(ctx, tx, deliverKind, deliverJob{DeliveryID: d.ID}); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Redeliver queues the payload of a past delivery again as a new delivery.
// The webhook must still exist (ErrNotFound) and be active (ErrInactive).
func (s *service) Redeliver(ctx context.Context, deliveryID string) (*Delivery, error) {
	old, err := s.delivery(ctx, deliveryID)
	if err != nil {
		return nil, err
	}
	w, err := s.Get(ctx, old.WebhookID)
	if err != nil {
		return nil, err
	}
	if !w.Active {
		return nil, ErrInactive
	}
	d, err := s.enqueue(ctx, old.WebhookID, old.Event, old.Payload)
	if err == nil {
		s.record(ctx, "webhook.redeliver", old.WebhookID, map[string]any{"delivery": old.ID, "new_delivery": d.ID})
	}
	return d, err
}

// Prune deletes deliveries older than keepDeliveries.
func (s *service) Prune(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE created_at < ?`, db.FormatTime(s.now().Add(-keepDeliveries)))
	if err != nil {
		return 0, fmt.Errorf("webhooks: prune: %w", err)
	}
	return res.RowsAffected()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
