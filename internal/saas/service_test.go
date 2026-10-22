package saas

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func setup(t *testing.T) (*Service, context.Context, context.Context) {
	t.Helper()
	d, e := db.Open(filepath.Join(t.TempDir(), "saas.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	if _, e = d.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,status TEXT DEFAULT 'active');CREATE TABLE roles(slug TEXT PRIMARY KEY);CREATE TABLE role_permissions(role TEXT,permission TEXT);INSERT INTO users(id) VALUES('alice'),('bob'),('staff');INSERT INTO role_permissions VALUES('admin','*');`); e != nil {
		t.Fatal(e)
	}
	data, e := migrations.ReadFile("migrations/0001_saas.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Exec(string(data)); e != nil {
		t.Fatal(e)
	}
	data, e = migrations.ReadFile("migrations/0002_native_management.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Exec(string(data)); e != nil {
		t.Fatal(e)
	}
	data, e = migrations.ReadFile("migrations/0003_subscription_versions.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Exec(string(data)); e != nil {
		t.Fatal(e)
	}
	if e = auth.ReloadRoles(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	_, e = d.Exec(`INSERT INTO saas_fleets VALUES('fleet','1','fleet-public','Fleet','active','','');INSERT INTO saas_apps(id,public_id,owner_id,fleet_id,name,slug,status,created_at,updated_at) VALUES('app','app-public','alice','fleet','Private app','app','active','','');INSERT INTO saas_entitlements(id,public_id,app_id,owner_id,subject_type,external_id,plan_id,status,limits_json,created_at,updated_at) VALUES('ent','ent-public','app','alice','user','source-subject','pro','active','{"requests":10}','','');`)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(d)
	s.Now = func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }
	return s, auth.WithUser(context.Background(), &auth.User{ID: "alice", Status: "active", Role: "customer"}), auth.WithUser(context.Background(), &auth.User{ID: "bob", Status: "active", Role: "customer"})
}
func TestOwnershipNoSecretsAndInactiveAccount(t *testing.T) {
	s, a, b := setup(t)
	o, e := s.Overview(a, false)
	if e != nil || len(o.Apps) != 1 || len(o.Entitlements) != 1 || o.Apps[0].FulfillmentEnabled {
		t.Fatalf("owner overview: %+v %v", o, e)
	}
	o, e = s.Overview(b, false)
	if e != nil || len(o.Apps) != 0 || len(o.Entitlements) != 0 {
		t.Fatalf("cross user leak: %+v %v", o, e)
	}
	if _, e = s.Overview(context.Background(), false); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.Overview(auth.WithUser(a, &auth.User{ID: "alice", Status: "disabled"}), false); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.Overview(a, true); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.Consume(b, "ent", ConsumeRequest{"requests", 1, "cross-owner-request"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/saas", nil).WithContext(a)
	w := httptest.NewRecorder()
	(&handlers{s}).page(false, nil)(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Private app") || strings.Contains(w.Body.String(), "secret_ciphertext") {
		t.Fatal("page private contract")
	}
	w = httptest.NewRecorder()
	(&handlers{s}).page(false, nil)(w, httptest.NewRequest("GET", "/saas", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestQuotaAtomicReplayConflictAndExpiry(t *testing.T) {
	s, a, _ := setup(t)
	req := ConsumeRequest{"requests", 4, "same-idempotency-key"}
	v, e := s.Consume(a, "ent", req)
	if e != nil || v.Quantity != 4 || v.Replayed {
		t.Fatal(v, e)
	}
	v, e = s.Consume(a, "ent", req)
	if e != nil || v.Quantity != 4 || !v.Replayed {
		t.Fatal(v, e)
	}
	req.Quantity = 5
	if _, e = s.Consume(a, "ent", req); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.Consume(a, "ent", ConsumeRequest{"requests", 7, "over-quota-request"}); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	if _, e = s.Consume(a, "ent", ConsumeRequest{"unknown", 1, "unknown-metric-request"}); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	s.DB.Exec(`UPDATE saas_entitlements SET valid_until='2025-01-01T00:00:00Z'`)
	if _, e = s.Consume(a, "ent", ConsumeRequest{"requests", 1, "expired-request-key"}); !errors.Is(e, ErrInactive) {
		t.Fatal(e)
	}
	var count int
	s.DB.QueryRow(`SELECT count(*) FROM saas_usage_events`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
func TestConcurrentConsumptionDoesNotExceedQuota(t *testing.T) {
	s, a, _ := setup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, key := range []string{"concurrent-request-1", "concurrent-request-2", "concurrent-request-3"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, e := s.Consume(a, "ent", ConsumeRequest{"requests", 4, key})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				ok++
			} else if !errors.Is(e, ErrQuota) {
				t.Errorf("unexpected %v", e)
			}
		}(key)
	}
	wg.Wait()
	if ok != 2 {
		t.Fatal(ok)
	}
	var quantity int
	s.DB.QueryRow(`SELECT quantity FROM saas_usage_counters`).Scan(&quantity)
	if quantity != 8 {
		t.Fatal(quantity)
	}
}
func TestLedgerAndNonceDenyUnconfiguredRevokedAndReplay(t *testing.T) {
	s, a, b := setup(t)
	hash := strings.Repeat("a", 64)
	replay, e := s.AcceptEvent(a, "app", "event-one", "webhook-request-one", hash)
	if e != nil || replay {
		t.Fatal(e)
	}
	replay, e = s.AcceptEvent(a, "app", "event-one", "webhook-request-one", hash)
	if e != nil || !replay {
		t.Fatal(e)
	}
	if _, e = s.AcceptEvent(a, "app", "event-two", "webhook-request-one", hash); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.AcceptEvent(b, "app", "event-three", "webhook-request-two", hash); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	_, e = s.DB.Exec(`INSERT INTO saas_key_versions(id,public_id,app_id,status,created_at,updated_at) VALUES('key','public-key','app','active','','')`)
	if e != nil {
		t.Fatal(e)
	}
	until := s.Now().Add(time.Minute)
	if e = s.ConsumeNonce(a, "app", "public-key", "nonce-long-enough", until); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	s.DB.Exec(`UPDATE saas_key_versions SET protocol='verified-test-contract'`)
	if e = s.ConsumeNonce(a, "app", "public-key", "nonce-long-enough", until); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeNonce(a, "app", "public-key", "nonce-long-enough", until); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	s.DB.Exec(`UPDATE saas_key_versions SET revoked_at='2026-10-14T00:00:00Z'`)
	if e = s.ConsumeNonce(a, "app", "public-key", "another-nonce-value", until); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestManagementStatusCAS(t *testing.T) {
	s, a, _ := setup(t)
	if e := s.UpdateStatus(a, "ent", StatusUpdate{"active", "suspended"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	admin := auth.WithUser(a, &auth.User{ID: "staff", Role: "admin", Status: "active"})
	if e := s.UpdateStatus(admin, "ent", StatusUpdate{"active", "suspended"}); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateStatus(admin, "ent", StatusUpdate{"active", "expired"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.Consume(a, "ent", ConsumeRequest{"requests", 1, "suspended-request"}); !errors.Is(e, ErrInactive) {
		t.Fatal(e)
	}
	if e := s.UpdateStatus(admin, "ent", StatusUpdate{"suspended", "arbitrary"}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
