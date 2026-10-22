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

// Billing-server reachability is a saved background observation: the
// health hook never calls the server and reports its age and state.
func TestHealthBillingServerUsesBackgroundObservation(t *testing.T) {
	e := boot(t)
	ctx := i18n.WithLocale(context.Background(), "en")
	srv := e.server()
	before := e.billing.CallCount("GET", "/catalog")
	checks := &[]admin.HealthCheck{}
	if _, err := e.svc.healthChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	var line admin.HealthCheck
	for _, c := range *checks {
		if strings.Contains(c.Name, "Fake") {
			line = c
		}
	}
	// Saving the server queued a refresh that has not run yet: the page says
	// so instead of calling the server or inventing a result.
	if line.Name == "" || line.Status != admin.CheckWarn || !strings.Contains(line.Detail, "check in progress") {
		t.Fatalf("%+v", line)
	}
	if e.billing.CallCount("GET", "/catalog") != before {
		t.Fatal("health page contacted the billing server")
	}
	// Saving the server queued one refresh; the worker runs it in the background.
	for i := 0; i < 5; i++ {
		if ran, _ := e.app.Deps.Jobs.RunOne(context.Background()); !ran {
			break
		}
	}
	checks = &[]admin.HealthCheck{}
	e.svc.healthChecks(ctx, checks)
	for _, c := range *checks {
		if strings.Contains(c.Name, "Fake") {
			line = c
		}
	}
	if line.Status != admin.CheckOK || !strings.Contains(line.Detail, "checked") {
		t.Fatalf("%+v", line)
	}
	if e.billing.CallCount("GET", "/catalog") != before+1 {
		t.Fatalf("catalog calls: %d", e.billing.CallCount("GET", "/catalog"))
	}
	// Deleting the server forgets its observation; the key no longer exists.
	if err := e.svc.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.observe.Get(context.Background(), ObservationKind, srv.ID); err == nil {
		t.Fatal("observation of a removed server still exists")
	}
}
