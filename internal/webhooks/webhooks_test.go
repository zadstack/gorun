package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

type sent struct {
	url    string
	header http.Header
	body   []byte
}

type fakeEndpoint struct {
	mu     sync.Mutex
	got    []sent
	status int
	err    error
}

func (f *fakeEndpoint) send(_ context.Context, url string, h http.Header, body []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, sent{url, h, body})
	return f.status, f.err
}

func testService(t *testing.T) (*service, *fakeEndpoint) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "webhooks", migrations); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ep := &fakeEndpoint{status: 200}
	s := &service{db: d, audit: audit.New(d, log), log: log, key: make([]byte, 32), now: time.Now,
		baseURL: func(context.Context) string { return "https://site.test" }, send: ep.send}
	return s, ep
}

func strp(s string) *string { return &s }

func TestCreateValidatesAndHidesSecretAtRest(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	_, _, err := s.Create(ctx, Input{Name: strp(""), URL: strp("http://example.com/x"), Events: []string{"nope"}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["name"] == "" || ve.Fields["url"] != "webhooks.error.https" || ve.Fields["events"] == "" {
		t.Fatalf("validation: %v %+v", err, ve)
	}
	if _, _, err := s.Create(ctx, Input{Name: strp("x"), URL: strp("https://user:pw@example.com/"), Events: []string{"*"}}); !errors.As(err, &ve) {
		t.Fatal("credentials in the URL must be rejected")
	}
	w, secret, err := s.Create(ctx, Input{Name: strp("CRM"), URL: strp("https://crm.example.com/hook"), Events: []string{"media.uploaded", "content.published", "content.published"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "whsec_") || !w.Active || strings.Join(w.Events, ",") != "content.published,media.uploaded" {
		t.Fatalf("created: %+v %q", w, secret)
	}
	var enc string
	_ = s.db.QueryRow(`SELECT secret_enc FROM webhooks WHERE id = ?`, w.ID).Scan(&enc)
	if enc == "" || strings.Contains(enc, secret) {
		t.Fatal("the secret must be stored encrypted")
	}
	if got, _ := s.secret(ctx, w.ID); got != secret {
		t.Fatal("the stored secret must decrypt to the issued one")
	}
	if all, _, _ := s.Create(ctx, Input{Name: strp("all"), URL: strp("https://a.example.com"), Events: []string{"media.deleted", "*"}}); strings.Join(all.Events, ",") != "*" {
		t.Fatalf("* must absorb other events: %v", all.Events)
	}
}

func TestDispatchSignsAndDeliversOnlyToSubscribers(t *testing.T) {
	s, ep := testService(t)
	ctx := context.Background()
	hook, secret, _ := s.Create(ctx, Input{Name: strp("a"), URL: strp("https://a.example.com/h"), Events: []string{"content.published"}})
	_, _, _ = s.Create(ctx, Input{Name: strp("b"), URL: strp("https://b.example.com/h"), Events: []string{"media.uploaded"}})
	off := false
	_, _, _ = s.Create(ctx, Input{Name: strp("c"), URL: strp("https://c.example.com/h"), Events: []string{"*"}, Active: &off})
	if err := s.dispatch(ctx, "content.published", json.RawMessage(`{"id":"p1","slug":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Deliveries(ctx, hook.ID, 10)
	if len(list) != 1 || list[0].Status != "queued" {
		t.Fatalf("deliveries: %+v", list)
	}
	// No queue attached in tests: run the job handler directly.
	b, _ := json.Marshal(deliverJob{DeliveryID: list[0].ID})
	if err := s.deliverHandler(ctx, b); err != nil {
		t.Fatal(err)
	}
	if len(ep.got) != 1 || ep.got[0].url != "https://a.example.com/h" {
		t.Fatalf("sent: %+v", ep.got)
	}
	h := ep.got[0].header
	ts, _ := strconv.ParseInt(h.Get("X-GoRun-Timestamp"), 10, 64)
	if !Verify(secret, h.Get("X-GoRun-Signature"), ts, ep.got[0].body, time.Now(), 5*time.Minute) {
		t.Fatal("signature must verify with the issued secret")
	}
	if Verify(secret, h.Get("X-GoRun-Signature"), ts, append(ep.got[0].body, ' '), time.Now(), 5*time.Minute) {
		t.Fatal("a modified body must not verify")
	}
	var env envelope
	if err := json.Unmarshal(ep.got[0].body, &env); err != nil || env.Event != "content.published" || env.Site != "https://site.test" || string(env.Data) != `{"id":"p1","slug":"hello"}` {
		t.Fatalf("envelope: %+v %v", env, err)
	}
	if h.Get("X-GoRun-Event") != "content.published" || h.Get("X-GoRun-Delivery") != list[0].ID {
		t.Fatalf("headers: %v", h)
	}
	after, _ := s.Deliveries(ctx, hook.ID, 10)
	if after[0].Status != "delivered" || after[0].Attempts != 1 || after[0].ResponseCode != 200 {
		t.Fatalf("recorded: %+v", after[0])
	}
}

func TestFailedDeliveriesRetryThenFailAndAutoDisable(t *testing.T) {
	s, ep := testService(t)
	ctx := context.Background()
	hook, _, _ := s.Create(ctx, Input{Name: strp("a"), URL: strp("https://a.example.com/h"), Events: []string{"*"}})
	ep.status = 500
	_ = s.dispatch(ctx, "menus.saved", map[string]string{"id": "m1"})
	list, _ := s.Deliveries(ctx, hook.ID, 1)
	job, _ := json.Marshal(deliverJob{DeliveryID: list[0].ID})
	for i := 1; i <= maxAttempts; i++ {
		err := s.deliverHandler(ctx, job)
		if err == nil {
			t.Fatalf("attempt %d: a 500 must ask the queue to retry", i)
		}
		d, _ := s.delivery(ctx, list[0].ID)
		want := "retrying"
		if i == maxAttempts {
			want = "failed"
		}
		if d.Status != want || d.Attempts != i || d.ResponseCode != 500 {
			t.Fatalf("attempt %d: %+v", i, d)
		}
	}
	if err := s.deliverHandler(ctx, job); err != nil {
		t.Fatal("a failed delivery must not be retried again")
	}
	w, _ := s.Get(ctx, hook.ID)
	if w.ConsecutiveFailures != 1 || !w.Active || w.LastStatus != "failed" {
		t.Fatalf("webhook after one failed delivery: %+v", w)
	}
	_, _ = s.db.Exec(`UPDATE webhooks SET consecutive_failures = ? WHERE id = ?`, disableAfter-1, hook.ID)
	_ = s.dispatch(ctx, "menus.saved", map[string]string{"id": "m2"})
	list, _ = s.Deliveries(ctx, hook.ID, 1)
	_, _ = s.db.Exec(`UPDATE webhook_deliveries SET attempts = ? WHERE id = ?`, maxAttempts-1, list[0].ID)
	job, _ = json.Marshal(deliverJob{DeliveryID: list[0].ID})
	_ = s.deliverHandler(ctx, job)
	if w, _ = s.Get(ctx, hook.ID); w.Active {
		t.Fatalf("webhook must be disabled after %d failed deliveries: %+v", disableAfter, w)
	}
	on := true
	if w, _ = s.Update(ctx, hook.ID, Input{Active: &on}); !w.Active || w.ConsecutiveFailures != 0 {
		t.Fatalf("re-enabling must reset the failure count: %+v", w)
	}
}

func TestTestRedeliverRotateDeleteAndPrune(t *testing.T) {
	s, ep := testService(t)
	ctx := context.Background()
	hook, secret, _ := s.Create(ctx, Input{Name: strp("a"), URL: strp("https://a.example.com/h"), Events: []string{"content.deleted"}})
	d, err := s.Test(ctx, hook.ID)
	if err != nil || d.Status != "delivered" || d.Event != TestEvent {
		t.Fatalf("test: %+v %v", d, err)
	}
	r, err := s.Redeliver(ctx, d.ID)
	if err != nil || r.ID == d.ID || r.Payload != d.Payload || r.Status != "queued" {
		t.Fatalf("redeliver: %+v %v", r, err)
	}
	next, err := s.RotateSecret(ctx, hook.ID)
	if err != nil || next == secret {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := s.Test(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	last := ep.got[len(ep.got)-1]
	ts, _ := strconv.ParseInt(last.header.Get("X-GoRun-Timestamp"), 10, 64)
	if !Verify(next, last.header.Get("X-GoRun-Signature"), ts, last.body, time.Now(), time.Minute) || Verify(secret, last.header.Get("X-GoRun-Signature"), ts, last.body, time.Now(), time.Minute) {
		t.Fatal("deliveries after rotation must be signed with the new secret only")
	}
	off := false
	_, _ = s.Update(ctx, hook.ID, Input{Active: &off})
	if _, err := s.Test(ctx, hook.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("testing a disabled webhook: %v", err)
	}
	_, _ = s.db.Exec(`UPDATE webhook_deliveries SET created_at = ? WHERE id = ?`, db.FormatTime(time.Now().Add(-keepDeliveries-time.Hour)), d.ID)
	if n, err := s.Prune(ctx); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if err := s.Delete(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = s.db.QueryRow(`SELECT count(*) FROM webhook_deliveries`).Scan(&left)
	if left != 0 {
		t.Fatalf("deleting a webhook must delete its deliveries, %d left", left)
	}
	if err := s.Delete(ctx, hook.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestVerifyRejectsStaleTimestamps(t *testing.T) {
	now := time.Now()
	body := []byte(`{"a":1}`)
	sig := Sign("whsec_x", now.Add(-10*time.Minute).Unix(), body)
	if Verify("whsec_x", sig, now.Add(-10*time.Minute).Unix(), body, now, 5*time.Minute) {
		t.Fatal("a 10-minute-old signature must be rejected with a 5-minute tolerance")
	}
}
