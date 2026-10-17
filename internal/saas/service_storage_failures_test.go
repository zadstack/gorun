package saas

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
)

func isSentinel(e error) bool {
	for _, s := range []error{ErrForbidden, ErrInvalid, ErrConflict, ErrInactive, ErrQuota} {
		if errors.Is(e, s) {
			return true
		}
	}
	return false
}

// Storage failures (schema drift, write rejected by a trigger) must surface as errors
// rather than being reported as success or masked as a validation/permission outcome.
func TestServiceSurfacesStorageFailures(t *testing.T) {
	newApp := AppInput{OwnerID: "bob", Name: "New", Slug: "new-app", Status: "active", Mode: "subscription", IdempotencyKey: "storage-app-request"}
	updApp := AppInput{OwnerID: "alice", Name: "Private app", Slug: "app", Status: "active", Mode: "subscription"}
	newSub := SubscriptionInput{AppID: "app", OwnerID: "bob", PlanID: "basic", Status: "active", IdempotencyKey: "storage-sub-request"}
	updSub := SubscriptionInput{AppID: "app", OwnerID: "alice", PlanID: "pro", Status: "active"}
	version := ""
	consume := ConsumeRequest{"requests", 1, "storage-consume-request"}
	expires := time.Date(2026, 10, 14, 0, 5, 0, 0, time.UTC)
	cases := []struct {
		name, sabotage string
		run            func(s *Service, owner, admin context.Context) error
	}{
		{"owner lookup", `ALTER TABLE users RENAME COLUMN status TO state`, func(s *Service, _, a context.Context) error { _, e := s.CreateApp(a, newApp); return e }},
		{"request lookup", `ALTER TABLE saas_configuration_requests RENAME COLUMN payload_hash TO digest`, func(s *Service, _, a context.Context) error { _, e := s.CreateApp(a, newApp); return e }},
		{"create app slug check", `ALTER TABLE saas_apps RENAME COLUMN slug TO handle`, func(s *Service, _, a context.Context) error { _, e := s.CreateApp(a, newApp); return e }},
		{"create app insert", `CREATE TRIGGER fail BEFORE INSERT ON saas_apps BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.CreateApp(a, newApp); return e }},
		{"create app request save", `CREATE TRIGGER fail BEFORE INSERT ON saas_configuration_requests BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.CreateApp(a, newApp); return e }},
		{"update app owner", `ALTER TABLE users RENAME COLUMN status TO state`, func(s *Service, _, a context.Context) error { _, e := s.UpdateApp(a, "app", updApp); return e }},
		{"update app slug check", `ALTER TABLE saas_apps RENAME COLUMN slug TO handle`, func(s *Service, _, a context.Context) error { _, e := s.UpdateApp(a, "app", updApp); return e }},
		{"update app write", `CREATE TRIGGER fail BEFORE UPDATE ON saas_apps BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.UpdateApp(a, "app", updApp); return e }},
		{"create sub request lookup", `ALTER TABLE saas_configuration_requests RENAME COLUMN payload_hash TO digest`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"create sub owner", `ALTER TABLE users RENAME COLUMN status TO state`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"create sub app check", `ALTER TABLE saas_apps RENAME COLUMN status TO state`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"create sub existing check", `ALTER TABLE saas_entitlements RENAME COLUMN status TO state`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"create sub insert", `CREATE TRIGGER fail BEFORE INSERT ON saas_entitlements BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"create sub request save", `CREATE TRIGGER fail BEFORE INSERT ON saas_configuration_requests BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.CreateSubscription(a, newSub); return e }},
		{"update sub write", `CREATE TRIGGER fail BEFORE UPDATE ON saas_entitlements BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { _, e := s.UpdateSubscription(a, "ent", updSub); return e }},
		{"delete source lookup", `ALTER TABLE saas_apps RENAME COLUMN source_id TO origin`, func(s *Service, _, a context.Context) error { return s.DeleteUnused(a, "app", "app") }},
		{"delete reference count", `ALTER TABLE saas_usage_events RENAME COLUMN entitlement_id TO ent`, func(s *Service, _, a context.Context) error { return s.DeleteUnused(a, "subscription", "ent") }},
		{"delete row", `CREATE TRIGGER fail BEFORE DELETE ON saas_entitlements BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { return s.DeleteUnused(a, "subscription", "ent") }},
		{"delete request history", `INSERT INTO saas_configuration_requests VALUES('staff','subscription','old-request-key-123','h','ent','');CREATE TRIGGER fail BEFORE DELETE ON saas_configuration_requests BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error { return s.DeleteUnused(a, "subscription", "ent") }},
		{"overview apps", `ALTER TABLE saas_apps RENAME COLUMN runtime TO engine`, func(s *Service, o, _ context.Context) error { _, e := s.Overview(o, false); return e }},
		{"overview entitlements", `ALTER TABLE saas_entitlements RENAME COLUMN plan_id TO plan`, func(s *Service, o, _ context.Context) error { _, e := s.Overview(o, false); return e }},
		{"overview usage", `ALTER TABLE saas_usage_counters RENAME COLUMN metric TO name`, func(s *Service, o, _ context.Context) error { _, e := s.Overview(o, false); return e }},
		{"overview usage scan", `INSERT INTO saas_usage_counters(id,app_id,subject_id,metric,quantity,created_at,updated_at) VALUES('u','app','source-subject','requests',2,'','');ALTER TABLE saas_usage_counters RENAME TO saas_usage_counters_real;CREATE VIEW saas_usage_counters AS SELECT app_id,subject_id,metric,'many' AS quantity FROM saas_usage_counters_real`, func(s *Service, o, _ context.Context) error { _, e := s.Overview(o, false); return e }},
		{"consume entitlement lookup", `ALTER TABLE saas_entitlements RENAME COLUMN external_id TO subject`, func(s *Service, o, _ context.Context) error { _, e := s.Consume(o, "ent", consume); return e }},
		{"consume replay lookup", `ALTER TABLE saas_usage_events RENAME COLUMN payload_hash TO digest`, func(s *Service, o, _ context.Context) error { _, e := s.Consume(o, "ent", consume); return e }},
		{"consume counter lookup", `ALTER TABLE saas_usage_counters RENAME COLUMN subject_id TO subject`, func(s *Service, o, _ context.Context) error { _, e := s.Consume(o, "ent", consume); return e }},
		{"consume counter write", `CREATE TRIGGER fail BEFORE INSERT ON saas_usage_counters BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, o, _ context.Context) error { _, e := s.Consume(o, "ent", consume); return e }},
		{"event app lookup", `ALTER TABLE saas_apps RENAME COLUMN owner_id TO owner`, func(s *Service, o, _ context.Context) error {
			_, e := s.AcceptEvent(o, "app", "evt-1", "event-idempotency-key", strings.Repeat("a", 64))
			return e
		}},
		{"event ledger lookup", `ALTER TABLE saas_webhook_ledger RENAME COLUMN event_id TO evt`, func(s *Service, o, _ context.Context) error {
			_, e := s.AcceptEvent(o, "app", "evt-1", "event-idempotency-key", strings.Repeat("a", 64))
			return e
		}},
		{"status write", `CREATE TRIGGER fail BEFORE UPDATE ON saas_entitlements BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error {
			return s.UpdateStatus(a, "ent", StatusUpdate{ExpectedStatus: "active", Status: "suspended"})
		}},
		{"nonce key lookup", `ALTER TABLE saas_key_versions RENAME COLUMN protocol TO proto`, func(s *Service, o, _ context.Context) error {
			return s.ConsumeNonce(o, "app", "key-pub", "nonce-value-0000001", expires)
		}},
		{"nonce replay lookup", `INSERT INTO saas_key_versions(id,public_id,app_id,status,protocol,created_at,updated_at) VALUES('k','key-pub','app','active','hmac-v1','','');ALTER TABLE saas_consumed_nonces RENAME COLUMN nonce TO value`, func(s *Service, o, _ context.Context) error {
			return s.ConsumeNonce(o, "app", "key-pub", "nonce-value-0000001", expires)
		}},
		{"services lookup", `ALTER TABLE saas_entitlements RENAME COLUMN valid_until TO expiry`, func(s *Service, _, a context.Context) error {
			_, e := s.UpdateServices(a, "ent", ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1})
			return e
		}},
		{"services write", `CREATE TRIGGER fail BEFORE UPDATE ON saas_entitlements BEGIN SELECT RAISE(ABORT,'blocked'); END`, func(s *Service, _, a context.Context) error {
			_, e := s.UpdateServices(a, "ent", ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1})
			return e
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, owner, _ := setup(t)
			admin := auth.WithUser(owner, staffUser)
			if _, e := s.DB.Exec(c.sabotage); e != nil {
				t.Fatal("sabotage:", e)
			}
			e := c.run(s, owner, admin)
			if e == nil || isSentinel(e) {
				t.Fatalf("want storage error, got %v", e)
			}
		})
	}
}

// A write silently skipped by the database (zero rows affected) must be reported as a conflict.
func TestUpdateServicesSkippedWriteIsConflict(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, staffUser)
	if _, e := s.DB.Exec(`CREATE TRIGGER skip BEFORE UPDATE ON saas_entitlements BEGIN SELECT RAISE(IGNORE); END`); e != nil {
		t.Fatal(e)
	}
	version := ""
	if _, e := s.UpdateServices(admin, "ent", ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var until string
	if e := s.DB.QueryRow(`SELECT valid_until FROM saas_entitlements WHERE id='ent'`).Scan(&until); e != nil || until != "" {
		t.Fatal(until, e)
	}
}

func TestOverviewListsOwnedUsage(t *testing.T) {
	s, owner, other := setup(t)
	if _, e := s.DB.Exec(`INSERT INTO saas_usage_counters(id,app_id,subject_id,metric,quantity,created_at,updated_at) VALUES('u','app','source-subject','requests',4,'','')`); e != nil {
		t.Fatal(e)
	}
	o, e := s.Overview(owner, false)
	if e != nil || len(o.Usage) != 1 || o.Usage[0].Quantity != 4 || o.Usage[0].Metric != "requests" {
		t.Fatal(o.Usage, e)
	}
	o, e = s.Overview(other, false)
	if e != nil || len(o.Usage) != 0 {
		t.Fatal("usage leaked to another account", o.Usage, e)
	}
}
