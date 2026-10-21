package saas

import (
	"context"
	"testing"

	"github.com/zadstack/gorun/internal/commerce"
)

func TestApplySubscriptionUpdatesOwnedEntitlement(t *testing.T) {
	s, _, _ := setup(t)
	ev := commerce.SubscriptionEvent{AccountID: "alice", Plan: "pro", Status: "past_due", GraceUntil: "2026-04-01T00:00:00Z", CurrentPeriodEnd: "2026-05-01T00:00:00Z"}
	if err := applySubscription(context.Background(), s.DB, ev); err != nil {
		t.Fatal(err)
	}
	var status, until string
	if err := s.DB.QueryRow(`SELECT status, valid_until FROM saas_entitlements WHERE id = 'ent'`).Scan(&status, &until); err != nil {
		t.Fatal(err)
	}
	if status != "grace" || until != ev.GraceUntil {
		t.Fatalf("entitlement = %s %s", status, until)
	}
	raw := []byte(`{"account_id":"alice","status":"canceled","reason":"subscription"}`)
	decoded, ok := subscriptionPayload(raw)
	if !ok || decoded.Status != "canceled" {
		t.Fatalf("payload = %+v %v", decoded, ok)
	}
	if err := applySubscription(context.Background(), s.DB, decoded); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id = 'ent'`).Scan(&status); err != nil || status != "suspended" {
		t.Fatalf("after cancel = %s %v", status, err)
	}
}
