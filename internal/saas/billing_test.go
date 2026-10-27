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
	raw := []byte(`{"account_id":"alice","plan":"pro","status":"canceled","reason":"subscription"}`)
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

func TestApplySubscriptionDoesNotTouchOtherPlans(t *testing.T) {
	s, _, _ := setup(t)
	if _, err := s.DB.Exec(`INSERT INTO saas_apps(id,public_id,owner_id,name,slug,status,mode,created_at,updated_at)
		VALUES('app-b','app-b-pub','alice','Other','app-b','active','subscription','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO saas_entitlements(id,public_id,app_id,owner_id,subject_type,external_id,plan_id,status,created_at,updated_at)
		VALUES('ent-b','ent-b-pub','app-b','alice','user','alice','basic','suspended','','')`); err != nil {
		t.Fatal(err)
	}
	ev := commerce.SubscriptionEvent{AccountID: "alice", Plan: "pro", Status: "active", CurrentPeriodEnd: "2026-06-01T00:00:00Z"}
	if err := applySubscription(context.Background(), s.DB, ev); err != nil {
		t.Fatal(err)
	}
	var pro, basic string
	if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id = 'ent'`).Scan(&pro); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id = 'ent-b'`).Scan(&basic); err != nil {
		t.Fatal(err)
	}
	if pro != "active" {
		t.Fatalf("matching plan = %s", pro)
	}
	if basic != "suspended" {
		t.Fatalf("other plan should stay suspended, got %s", basic)
	}
	// A cancel without plan must not broadcast when plan is empty and we
	// still require a plan match only when Plan is set — empty Plan updates
	// every matching owner row (legacy events). Prefer plan-scoped events.
	evOther := commerce.SubscriptionEvent{AccountID: "alice", Plan: "basic", Status: "active", CurrentPeriodEnd: "2026-07-01T00:00:00Z"}
	if err := applySubscription(context.Background(), s.DB, evOther); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id = 'ent-b'`).Scan(&basic); err != nil || basic != "active" {
		t.Fatalf("basic after its own activate = %s %v", basic, err)
	}
	if err := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id = 'ent'`).Scan(&pro); err != nil || pro != "active" {
		t.Fatalf("pro must stay active = %s %v", pro, err)
	}
}
