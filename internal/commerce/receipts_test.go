package commerce

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestReceiptReplayConcurrentWorkers(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "receipt", 1000)
	req := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "customer@example.test", IdempotencyKey: "receipt-concurrent-replay"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.SubmitCart(ctx, req); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := e.count(`SELECT count(*) FROM commerce_request_receipts`); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if len(e.mail.all()) != 0 {
		t.Fatal("checkout blocked on synchronous mail")
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.svc.deliverRequestReceipts(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	o, err := e.svc.SubmitCart(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.svc.deliverRequestReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	sent := e.mail.all()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, o.Number) {
		t.Fatalf("mail %#v", sent)
	}
	if o.ReceiptStatus != "sent" || o.PaidAt != "" || o.Status != OrderPending || e.count(`SELECT count(*) FROM entitlements`) != 0 || len(e.stripe.reqs) != 0 {
		t.Fatalf("unsafe lifecycle %+v", o)
	}
	for _, locale := range []string{"ar", "en"} {
		sub, html, text := requestReceiptEmail(e.svc.d.I18n, locale, o.Number, "https://example.test")
		if !strings.HasPrefix(sub, "example.test — ") || !strings.HasPrefix(text, "example.test\n") || strings.Contains(html, "<a ") || !strings.Contains(sub, o.Number) || !strings.Contains(text, o.Number) || strings.Contains(text, "commerce.mail.") || !strings.Contains(html, "https://example.test") {
			t.Fatalf("invalid %s receipt", locale)
		}
	}
}

func TestRequestReceiptUncertainNeverResent(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "uncertain", 1000)
	req := CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "customer@example.test", IdempotencyKey: "uncertain-receipt-unique"}
	if _, err := e.svc.SubmitCart(ctx, req); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.svc.sendReceipt = func(context.Context, string, string, string, string) error {
		calls++
		return errors.New("transport failure secret-token")
	}
	for range 3 {
		if err := e.svc.deliverRequestReceipts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	o, err := e.svc.SubmitCart(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || o.ReceiptStatus != "uncertain" || strings.Contains(o.ReceiptError, "secret-token") {
		t.Fatalf("unsafe resend calls=%d order=%+v", calls, o)
	}
}

func TestRequestReceiptInterruptedDispatchAndNoBackfill(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	p := serviceProduct(t, e, "interrupted", 1000)
	o, err := e.svc.SubmitCart(ctx, CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "customer@example.test", IdempotencyKey: "interrupted-receipt-unique"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.db.Exec(`UPDATE commerce_request_receipts SET status='sending',updated_at=? WHERE order_id=?`, time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339), o.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.svc.deliverRequestReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	o, err = e.svc.Order(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.ReceiptStatus != "uncertain" || len(e.mail.all()) != 0 {
		t.Fatalf("interrupted receipt resent %+v", o)
	}
	// Legacy orders without outbox rows are never scanned or backfilled.
	if _, err = e.svc.db.Exec(`DELETE FROM commerce_request_receipts WHERE order_id=?`, o.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.svc.deliverRequestReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM commerce_request_receipts`) != 0 || len(e.mail.all()) != 0 {
		t.Fatal("legacy receipt backfilled")
	}
}
