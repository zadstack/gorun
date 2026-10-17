package commerce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRefundWhileAccountCreationIsInFlight(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	upstream := e.billing.Config.Handler
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/mcp-billing/v1/accounts" {
			close(entered)
			<-release
		}
		upstream.ServeHTTP(w, r)
	}))
	defer func() { unblock(); proxy.Close() }()
	srv, err := e.svc.saveServer(ctx, Server{Name: "Concurrent", BaseURL: proxy.URL, ClientID: testClientID, Secret: testServerSecret})
	if err != nil {
		t.Fatal(err)
	}
	id := e.checkout(e.product("race", srv), "race@example.com")
	e.pay(id)
	done := make(chan error, 1)
	go func() { done <- e.svc.provision(ctx, json.RawMessage(`{"order_id":"`+id+`"}`)) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("create did not start")
	}
	b, h := stripeEvent("evt_refund_race", evChargeRefunded, map[string]any{"id": "ch_race", "payment_intent": "pi_" + id, "refunded": true})
	if r := e.webhook(b, h); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if errs := e.drain(JobSuspend); len(errs) > 0 {
		t.Fatal(errs)
	}
	order, _ := e.svc.Order(ctx, id)
	ent, _, _ := e.svc.Entitlements(ctx, ListQuery{})
	acct, ok := e.billing.Account(order.CustomerID)
	if order.Status != OrderRefunded || len(ent) != 1 || ent[0].Status != EntitlementSuspended || !ok || acct.Status != "suspended" {
		t.Fatalf("refund recovery: order=%s entitlement count=%d remote=%s exists=%v", order.Status, len(ent), acct.Status, ok)
	}
}
