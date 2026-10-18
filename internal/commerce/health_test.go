package commerce

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/i18n"
)

// Stored Stripe credentials must not report CheckOK: presence ≠ live checkout.
func TestHealthStripeCredentialsAreWarnNotOK(t *testing.T) {
	e := boot(t)
	ctx := i18n.WithLocale(context.Background(), "en")
	checks := &[]admin.HealthCheck{}
	if _, err := e.svc.healthChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	byName := map[string]admin.HealthCheck{}
	for _, c := range *checks {
		byName[c.Name] = c
	}
	stripe, ok := byName["Stripe secret key"]
	if !ok {
		t.Fatalf("missing stripe check in %+v", *checks)
	}
	if stripe.Status != admin.CheckWarn {
		t.Fatalf("stripe status = %s (stored key must not claim ok)", stripe.Status)
	}
	if !strings.Contains(stripe.Detail, "checkout not verified") {
		t.Fatalf("stripe detail = %q", stripe.Detail)
	}
	webhook, ok := byName["Stripe webhook secret"]
	if !ok {
		t.Fatalf("missing webhook check in %+v", *checks)
	}
	if webhook.Status != admin.CheckWarn {
		t.Fatalf("webhook status = %s", webhook.Status)
	}
	if !strings.Contains(webhook.Detail, "delivery not verified") {
		t.Fatalf("webhook detail = %q", webhook.Detail)
	}
}
