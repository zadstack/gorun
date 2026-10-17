package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRedeliverChecksTheWebhook(t *testing.T) {
	s, ep := testService(t)
	ctx := context.Background()
	hook, _, _ := s.Create(ctx, Input{Name: strp("a"), URL: strp("https://a.example.com/h"), Events: []string{"*"}})
	tested, err := s.Test(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeliver(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown delivery: %v", err)
	}
	// A delivery whose webhook row is gone (FKs off, or a race with delete).
	conn, err := s.db.Conn(ctx) // the pragma is per connection
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`PRAGMA foreign_keys = OFF`,
		`INSERT INTO webhook_deliveries (id, webhook_id, event, payload, status, created_at) VALUES ('orphan', 'gone', 'menus.saved', '{}', 'failed', '2026-01-01T00:00:00Z')`,
		`PRAGMA foreign_keys = ON`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	if _, err := s.Redeliver(ctx, "orphan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delivery of a deleted webhook: %v", err)
	}

	off := false
	_, _ = s.Update(ctx, hook.ID, Input{Active: &off})
	if _, err := s.Redeliver(ctx, tested.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("redelivering to a disabled webhook: %v", err)
	}

	// A delivery already queued when the webhook was disabled (even a
	// webhook.test payload) is not sent and does not count as a failure.
	sentBefore := len(ep.got)
	queued, err := s.enqueue(ctx, hook.ID, TestEvent, tested.Payload)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(deliverJob{DeliveryID: queued.ID})
	if err := s.deliverHandler(ctx, job); err != nil {
		t.Fatal(err)
	}
	if len(ep.got) != sentBefore {
		t.Fatal("nothing may be sent to a disabled webhook outside Test")
	}
	d, _ := s.delivery(ctx, queued.ID)
	w, _ := s.Get(ctx, hook.ID)
	if d.Status != "failed" || d.Error != "webhook disabled" || w.ConsecutiveFailures != 0 {
		t.Fatalf("skipped delivery: %+v, webhook failures %d", d, w.ConsecutiveFailures)
	}
	q2, _ := s.enqueue(ctx, hook.ID, "menus.saved", `{}`)
	job, _ = json.Marshal(deliverJob{DeliveryID: q2.ID})
	_ = s.deliverHandler(ctx, job)
	if w, _ = s.Get(ctx, hook.ID); w.ConsecutiveFailures != 0 {
		t.Fatalf("a skipped delivery must not count towards auto-disable: %d", w.ConsecutiveFailures)
	}
}
