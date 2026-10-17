package commerce

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

// A manual request and a Stripe payment can both be pending; the receipt must
// distinguish them without changing either order's lifecycle.
func TestManualReceiptDoesNotClaimPaymentOrActivation(t *testing.T) {
	e := boot(t)
	p := serviceProduct(t, e, "review-copy", 1200)
	o, err := e.svc.SubmitCart(context.Background(), CartCheckout{Items: []CartItem{{p.ID, 1}}, Email: "review@example.test", Name: "Review", IdempotencyKey: "review-copy-unique-request"})
	if err != nil {
		t.Fatal(err)
	}
	v := viewOf(o)
	if !v.ManualRequest || !v.Final || v.Active || o.PaidAt != "" {
		t.Fatalf("incorrect receipt state: %+v", v)
	}
	for _, locale := range []string{"ar", "en"} {
		ctx := i18n.WithLocale(context.Background(), locale)
		body := string(e.svc.statusFragment(ctx, v))
		mustContain(t, body, e.svc.d.I18n.T(locale, "commerce.request.received"), e.svc.d.I18n.T(locale, "commerce.request.help"))
		if strings.Contains(body, `hx-trigger=`) || strings.Contains(body, e.svc.d.I18n.T(locale, "commerce.order.status.pending")) {
			t.Fatalf("manual receipt misleading: %s", body)
		}
		paidFlow := viewOf(&Order{ID: "paid-flow", Status: OrderPending})
		body = string(e.svc.statusFragment(ctx, paidFlow))
		mustContain(t, body, e.svc.d.I18n.T(locale, "commerce.order.status.pending"), `hx-trigger="every 3s"`)
	}
	if len(e.stripe.reqs) != 0 || e.count(`SELECT count(*) FROM jobs WHERE kind='commerce.provision'`) != 0 {
		t.Fatal("request triggered payment or activation")
	}
}
