package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// Event is a site event a webhook can subscribe to.
type Event struct {
	Name  string            `json:"name"`
	Label map[string]string `json:"label"`
}

// TestEvent is sent by Test; webhooks need not subscribe to it.
const TestEvent = "webhook.test"

// events is the catalog of subscribable events (hook names emitted by the core).
var events = []Event{
	{"content.published", map[string]string{"en": "Content published", "ar": "نشر محتوى"}},
	{"content.deleted", map[string]string{"en": "Content deleted", "ar": "حذف محتوى"}},
	{"content.comment_submitted", map[string]string{"en": "Comment submitted", "ar": "تعليق جديد"}},
	{"media.uploaded", map[string]string{"en": "Media uploaded", "ar": "رفع وسائط"}},
	{"media.deleted", map[string]string{"en": "Media deleted", "ar": "حذف وسائط"}},
	{"menus.saved", map[string]string{"en": "Menu saved", "ar": "حفظ قائمة"}},
	{"redirects.saved", map[string]string{"en": "Redirects changed", "ar": "تغيير التحويلات"}},
	{"site.form.submitted", map[string]string{"en": "Form submitted", "ar": "إرسال نموذج"}},
	{"user.login", map[string]string{"en": "User signed in", "ar": "تسجيل دخول مستخدم"}},
	{"commerce.order.paid", map[string]string{"en": "Order paid", "ar": "دفع طلب"}},
	{"commerce.entitlement.activated", map[string]string{"en": "Entitlement activated", "ar": "تفعيل اشتراك"}},
	{"migration.item.imported", map[string]string{"en": "Migration item imported", "ar": "استيراد عنصر ترحيل"}},
}

// Events returns the subscribable events.
func Events() []Event { return events }

func knownEvent(name string) bool {
	for _, e := range events {
		if e.Name == name {
			return true
		}
	}
	return false
}

// envelope is the JSON body of every delivery.
type envelope struct {
	ID        string          `json:"id"`
	Event     string          `json:"event"`
	CreatedAt string          `json:"created_at"`
	Site      string          `json:"site"`
	Data      json.RawMessage `json:"data"`
}

type deliverJob struct {
	DeliveryID string `json:"delivery_id"`
}

func rawPayload(payload any) json.RawMessage {
	switch p := payload.(type) {
	case json.RawMessage:
		return p
	case []byte:
		return p
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}

func (s *service) envelope(ctx context.Context, event string, data json.RawMessage) (string, error) {
	site := ""
	if s.baseURL != nil {
		site = s.baseURL(ctx)
	}
	b, err := json.Marshal(envelope{ID: db.NewID(), Event: event, CreatedAt: s.now().UTC().Format(time.RFC3339), Site: site, Data: data})
	if err != nil {
		return "", err
	}
	if len(b) > maxPayload {
		b, _ = json.Marshal(envelope{ID: db.NewID(), Event: event, CreatedAt: s.now().UTC().Format(time.RFC3339), Site: site,
			Data: json.RawMessage(`{"truncated":true}`)})
	}
	return string(b), nil
}

// dispatch queues one delivery per active webhook subscribed to event, all
// in one transaction: either every delivery is queued or none is. It
// returns an error only when queuing failed, so the hook retries the event
// without duplicating deliveries already queued by an earlier try.
func (s *service) dispatch(ctx context.Context, event string, payload any) error {
	hooks, err := s.List(ctx)
	if err != nil {
		return err
	}
	var targets []string
	for _, w := range hooks {
		if w.Active && (contains(w.Events, "*") || contains(w.Events, event)) {
			targets = append(targets, w.ID)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	body, err := s.envelope(ctx, event, rawPayload(payload))
	if err != nil {
		return err
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, id := range targets {
			if _, err := s.enqueueTx(ctx, tx, id, event, body); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("webhooks: dispatch %s: %w", event, err)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Sign returns the X-GoRun-Signature value for body sent at timestamp ts:
// "sha256=" + hex(HMAC-SHA256(secret, ts + "." + body)).
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a signature header against body, rejecting timestamps older
// than tolerance (receivers can use it; it is also the reference for docs).
func Verify(secret, signature string, ts int64, body []byte, now time.Time, tolerance time.Duration) bool {
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return false
	}
	return hmac.Equal([]byte(signature), []byte(Sign(secret, ts, body)))
}

// sender posts a signed delivery; tests replace it.
type sender func(ctx context.Context, url string, header http.Header, body []byte) (status int, err error)

func httpSender(c *http.Client) sender {
	return func(ctx context.Context, url string, header http.Header, body []byte) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header = header
		resp, err := c.Do(req)
		if err != nil {
			// Drop the URL (its query may hold a token) from the recorded error.
			var ue *neturl.Error
			if errors.As(err, &ue) {
				return 0, fmt.Errorf("%s: %w", ue.Op, ue.Err)
			}
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, nil
	}
}

// attempt sends delivery d once and records the outcome. It returns an
// error when the endpoint did not accept the delivery (non-2xx or network).
// Only the synchronous Test path (sync) may send to a disabled webhook.
func (s *service) attempt(ctx context.Context, d *Delivery, final, sync bool) error {
	w, err := s.Get(ctx, d.WebhookID)
	if errors.Is(err, ErrNotFound) {
		return nil // webhook deleted: its deliveries went with it
	}
	if err != nil {
		return err
	}
	if !w.Active && !sync {
		// Skipped, not failed: it must not count towards auto-disable.
		s.skip(ctx, d, "webhook disabled")
		return nil
	}
	secret, err := s.secret(ctx, w.ID)
	if err != nil {
		s.finish(ctx, d, w, "failed", 0, "secret unavailable", 0)
		return nil
	}
	ts := s.now().Unix()
	body := []byte(d.Payload)
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "GoRun-Webhooks/1")
	h.Set("X-GoRun-Event", d.Event)
	h.Set("X-GoRun-Delivery", d.ID)
	h.Set("X-GoRun-Timestamp", strconv.FormatInt(ts, 10))
	h.Set("X-GoRun-Signature", Sign(secret, ts, body))
	start := s.now()
	code, err := s.send(ctx, w.URL, h, body)
	ms := int(s.now().Sub(start).Milliseconds())
	switch {
	case err == nil && code >= 200 && code < 300:
		s.finish(ctx, d, w, "delivered", code, "", ms)
		return nil
	case errors.Is(err, httpx.ErrRedirectRefused):
		// Permanent: the endpoint URL is wrong; resending would not help.
		s.finish(ctx, d, w, "failed", code, "endpoint answered with a redirect; webhooks do not follow redirects, use the final URL", ms)
		return nil
	case err != nil:
		err = fmt.Errorf("request failed: %w", err)
	default:
		err = fmt.Errorf("endpoint answered %d", code)
	}
	status := "retrying"
	if final {
		status = "failed"
	}
	s.finish(ctx, d, w, status, code, err.Error(), ms)
	return err
}

// skip marks d failed without touching the webhook's status or counters.
func (s *service) skip(ctx context.Context, d *Delivery, msg string) {
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE webhook_deliveries SET status = 'failed', error = ? WHERE id = ?`, msg, d.ID); err != nil {
		s.log.Error("webhooks: record delivery", "err", err)
	}
}

func (s *service) finish(ctx context.Context, d *Delivery, w *Webhook, status string, code int, msg string, ms int) {
	ctx = context.WithoutCancel(ctx)
	now := db.FormatTime(s.now())
	delivered := ""
	if status == "delivered" {
		delivered = now
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = ?, attempts = attempts + 1, response_code = ?, error = ?, duration_ms = ?, delivered_at = ? WHERE id = ?`,
		status, code, msg, ms, delivered, d.ID); err != nil {
		s.log.Error("webhooks: record delivery", "err", err)
	}
	if d.Event == TestEvent {
		_, _ = s.db.ExecContext(ctx, `UPDATE webhooks SET last_status = ?, last_delivery_at = ? WHERE id = ?`, status, now, w.ID)
		return
	}
	if status == "delivered" {
		_, _ = s.db.ExecContext(ctx, `UPDATE webhooks SET last_status = ?, last_delivery_at = ?, consecutive_failures = 0 WHERE id = ?`, status, now, w.ID)
		return
	}
	if status != "failed" {
		_, _ = s.db.ExecContext(ctx, `UPDATE webhooks SET last_status = ?, last_delivery_at = ? WHERE id = ?`, status, now, w.ID)
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE webhooks SET last_status = ?, last_delivery_at = ?, consecutive_failures = consecutive_failures + 1,
		active = CASE WHEN consecutive_failures + 1 >= ? THEN 0 ELSE active END WHERE id = ?`, status, now, disableAfter, w.ID)
	if w.Active && w.ConsecutiveFailures+1 >= disableAfter {
		s.record(ctx, "webhook.auto_disable", w.ID, map[string]any{"consecutive_failures": w.ConsecutiveFailures + 1})
	}
}

// deliverHandler is the jobs handler: one attempt per run, retried with the
// queue's backoff until maxAttempts.
func (s *service) deliverHandler(ctx context.Context, payload json.RawMessage) error {
	var j deliverJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return nil // malformed job: nothing to retry
	}
	d, err := s.delivery(ctx, j.DeliveryID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Status == "delivered" || d.Status == "failed" {
		return nil
	}
	return s.attempt(ctx, d, d.Attempts+1 >= maxAttempts, false)
}

// Test sends a webhook.test event synchronously (one attempt, no retry) and
// returns the recorded delivery.
func (s *service) Test(ctx context.Context, id string) (*Delivery, error) {
	w, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !w.Active {
		return nil, ErrInactive
	}
	body, err := s.envelope(ctx, TestEvent, json.RawMessage(`{"message":"GoRun webhook test"}`))
	if err != nil {
		return nil, err
	}
	d := &Delivery{ID: db.NewID(), WebhookID: id, Event: TestEvent, Status: "queued", CreatedAt: db.FormatTime(s.now()), Payload: body}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event, payload, status, created_at) VALUES (?, ?, ?, ?, 'queued', ?)`,
		d.ID, d.WebhookID, d.Event, d.Payload, d.CreatedAt); err != nil {
		return nil, fmt.Errorf("webhooks: test delivery: %w", err)
	}
	_ = s.attempt(ctx, d, true, true)
	s.record(ctx, "webhook.test", id, map[string]any{"delivery": d.ID})
	return s.delivery(ctx, d.ID)
}
