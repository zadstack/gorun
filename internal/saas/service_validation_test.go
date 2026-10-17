package saas

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
)

func TestManagementServiceRejectsInvalidInputBeforeWriting(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, staffUser)
	validApp := AppInput{OwnerID: "bob", Name: "Valid", Slug: "valid-app", Status: "active", Mode: "subscription", IdempotencyKey: "valid-app-request-key"}
	appCases := map[string]func(*AppInput){
		"blank name":   func(q *AppInput) { q.Name = "  " },
		"bad slug":     func(q *AppInput) { q.Slug = "Bad Slug" },
		"bad status":   func(q *AppInput) { q.Status = "deleted" },
		"bad mode":     func(q *AppInput) { q.Mode = "hosted" },
		"ftp base url": func(q *AppInput) { q.BaseURL = "ftp://example.test" },
		"short key":    func(q *AppInput) { q.IdempotencyKey = "short" },
		"long key":     func(q *AppInput) { q.IdempotencyKey = strings.Repeat("k", 201) },
	}
	for name, mutate := range appCases {
		q := validApp
		mutate(&q)
		if _, e := s.CreateApp(admin, q); !errors.Is(e, ErrInvalid) {
			t.Fatalf("create app %s: %v", name, e)
		}
	}
	dup := validApp
	dup.Slug = "app"
	if _, e := s.CreateApp(admin, dup); !errors.Is(e, ErrConflict) {
		t.Fatal("duplicate slug", e)
	}
	upd := AppInput{OwnerID: "alice", Name: "Private app", Slug: "app", Status: "active", Mode: "subscription"}
	if _, e := s.UpdateApp(owner, "app", upd); !errors.Is(e, ErrForbidden) {
		t.Fatal("customer update app", e)
	}
	bad := upd
	bad.Name = ""
	if _, e := s.UpdateApp(admin, "app", bad); !errors.Is(e, ErrInvalid) {
		t.Fatal("invalid update app", e)
	}
	bad = upd
	bad.OwnerID = "ghost"
	if _, e := s.UpdateApp(admin, "app", bad); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown owner", e)
	}
	if _, e := s.CreateApp(admin, validApp); e != nil {
		t.Fatal(e)
	}
	bad = upd
	bad.Slug = "valid-app"
	if _, e := s.UpdateApp(admin, "app", bad); !errors.Is(e, ErrConflict) {
		t.Fatal("slug taken by another app", e)
	}
	var n int
	if e := s.DB.QueryRow(`SELECT count(*) FROM saas_apps`).Scan(&n); e != nil || n != 2 {
		t.Fatal("only the valid app is stored", n, e)
	}
}

func TestSubscriptionServiceValidationAndConflicts(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, staffUser)
	base := SubscriptionInput{AppID: "app", OwnerID: "bob", PlanID: "basic", Status: "active", Limits: map[string]int64{"tasks": 1}, IdempotencyKey: "subscription-request-key"}
	if _, e := s.CreateSubscription(owner, base); !errors.Is(e, ErrForbidden) {
		t.Fatal("customer create", e)
	}
	invalid := []func(*SubscriptionInput){
		func(q *SubscriptionInput) { q.PlanID = " " },
		func(q *SubscriptionInput) { q.Status = "unknown" },
		func(q *SubscriptionInput) { q.Limits = map[string]int64{"tasks": -1} },
		func(q *SubscriptionInput) { q.Features = map[string]bool{"": true} },
		func(q *SubscriptionInput) { q.ValidUntil = "next year" },
		func(q *SubscriptionInput) { q.OwnerID = "ghost" },
		func(q *SubscriptionInput) { q.AppID = "missing-app" },
	}
	for i, mutate := range invalid {
		q := base
		mutate(&q)
		if _, e := s.CreateSubscription(admin, q); !errors.Is(e, ErrInvalid) {
			t.Fatalf("create case %d: %v", i, e)
		}
	}
	dup := base
	dup.OwnerID = "alice"
	dup.IdempotencyKey = "duplicate-active-subscription"
	if _, e := s.CreateSubscription(admin, dup); !errors.Is(e, ErrConflict) {
		t.Fatal("second active subscription", e)
	}
	first, e := s.CreateSubscription(admin, base)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.CreateSubscription(admin, base)
	if e != nil || !replay.Replayed || replay.ID != first.ID {
		t.Fatal("replay", replay, e)
	}

	upd := SubscriptionInput{AppID: "app", OwnerID: "bob", PlanID: "pro", Status: "active", ExpectedVersion: first.Version}
	if _, e = s.UpdateSubscription(owner, first.ID, upd); !errors.Is(e, ErrForbidden) {
		t.Fatal("customer update", e)
	}
	updInvalid := []func(*SubscriptionInput){
		func(q *SubscriptionInput) { q.PlanID = "" },
		func(q *SubscriptionInput) { q.Limits = map[string]int64{"tasks": -5} },
		func(q *SubscriptionInput) { q.Features = map[string]bool{strings.Repeat("f", 101): true} },
		func(q *SubscriptionInput) { q.ValidUntil = "2027-13-01" },
	}
	for i, mutate := range updInvalid {
		q := upd
		mutate(&q)
		if _, e = s.UpdateSubscription(admin, first.ID, q); !errors.Is(e, ErrInvalid) {
			t.Fatalf("update case %d: %v", i, e)
		}
	}
	upd.Features = map[string]bool{"priority": true}
	upd.ValidUntil = "2027-01-01T00:00:00Z"
	if _, e = s.UpdateSubscription(admin, first.ID, upd); e != nil {
		t.Fatal(e)
	}
	var features, limits, until string
	if e = s.DB.QueryRow(`SELECT features_json,limits_json,valid_until FROM saas_entitlements WHERE id=?`, first.ID).Scan(&features, &limits, &until); e != nil || features != `{"priority":true}` || limits != "{}" || until != "2027-01-01T00:00:00Z" {
		t.Fatal(features, limits, until, e)
	}
}

func TestDeleteUnusedGuards(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, staffUser)
	if e := s.DeleteUnused(owner, "app", "app"); !errors.Is(e, ErrForbidden) {
		t.Fatal("customer delete", e)
	}
	if e := s.DeleteUnused(admin, "fleet", "fleet"); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown kind", e)
	}
	if e := s.DeleteUnused(admin, "subscription", "missing"); !errors.Is(e, ErrConflict) {
		t.Fatal("missing subscription", e)
	}
	if _, e := s.DB.Exec(`UPDATE saas_entitlements SET source_id='imported-1' WHERE id='ent'`); e != nil {
		t.Fatal(e)
	}
	if e := s.DeleteUnused(admin, "subscription", "ent"); !errors.Is(e, ErrConflict) {
		t.Fatal("imported subscription must be preserved", e)
	}
	var n int
	if e := s.DB.QueryRow(`SELECT count(*) FROM saas_entitlements WHERE id='ent'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestStatusEventAndNonceGuards(t *testing.T) {
	s, owner, _ := setup(t)
	anon := context.Background()
	hash := strings.Repeat("a", 64)
	if e := s.UpdateStatus(anon, "ent", StatusUpdate{ExpectedStatus: "active", Status: "revoked"}); !errors.Is(e, ErrForbidden) {
		t.Fatal("anonymous status", e)
	}
	if _, e := s.Consume(anon, "ent", ConsumeRequest{"requests", 1, "anonymous-consume-key"}); !errors.Is(e, ErrForbidden) {
		t.Fatal("anonymous consume", e)
	}
	if _, e := s.Consume(owner, "ent", ConsumeRequest{" requests", 1, "padded-metric-request"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("padded metric", e)
	}
	if _, e := s.AcceptEvent(anon, "app", "evt", "event-idempotency-key", hash); !errors.Is(e, ErrForbidden) {
		t.Fatal("anonymous event", e)
	}
	if _, e := s.AcceptEvent(owner, "app", "", "event-idempotency-key", hash); !errors.Is(e, ErrInvalid) {
		t.Fatal("empty event", e)
	}
	if _, e := s.AcceptEvent(owner, "app", "evt", "event-idempotency-key", strings.Repeat("z", 64)); !errors.Is(e, ErrInvalid) {
		t.Fatal("non-hex hash", e)
	}
	expires := s.Now().Add(5 * time.Minute)
	if e := s.ConsumeNonce(anon, "app", "key-pub", "nonce-value-0000001", expires); !errors.Is(e, ErrForbidden) {
		t.Fatal("anonymous nonce", e)
	}
	if e := s.ConsumeNonce(owner, "app", "key-pub", "short", expires); !errors.Is(e, ErrInvalid) {
		t.Fatal("short nonce", e)
	}
	if e := s.ConsumeNonce(owner, "app", "key-pub", "nonce-value-0000001", s.Now().Add(time.Hour)); !errors.Is(e, ErrInvalid) {
		t.Fatal("far expiry", e)
	}
	if e := s.ConsumeNonce(owner, "app", "missing-key", "nonce-value-0000001", expires); !errors.Is(e, ErrForbidden) {
		t.Fatal("unknown key", e)
	}
	if _, e := s.DB.Exec(`INSERT INTO saas_key_versions(id,public_id,app_id,status,protocol,grace_expires_at,created_at,updated_at) VALUES('k','key-pub','app','grace','hmac-v1','2026-10-13T00:00:00Z','','')`); e != nil {
		t.Fatal(e)
	}
	if e := s.ConsumeNonce(owner, "app", "key-pub", "nonce-value-0000001", expires); !errors.Is(e, ErrForbidden) {
		t.Fatal("expired grace key", e)
	}
	if _, e := s.DB.Exec(`UPDATE saas_key_versions SET grace_expires_at='2026-10-15T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if e := s.ConsumeNonce(owner, "app", "key-pub", "nonce-value-0000001", expires); e != nil {
		t.Fatal("grace key within window", e)
	}
	if e := s.ConsumeNonce(owner, "app", "key-pub", "nonce-value-0000001", expires); !errors.Is(e, ErrConflict) {
		t.Fatal("nonce replay", e)
	}
}

func TestConsumeRejectsBadStoredExpiryAndLimits(t *testing.T) {
	s, owner, _ := setup(t)
	if _, e := s.DB.Exec(`UPDATE saas_entitlements SET valid_until='soon'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Consume(owner, "ent", ConsumeRequest{"requests", 1, "bad-expiry-consume-key"}); !errors.Is(e, ErrInactive) {
		t.Fatal("unparseable expiry must deny", e)
	}
	if _, e := s.DB.Exec(`UPDATE saas_entitlements SET valid_until='',limits_json='[1,2]'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Consume(owner, "ent", ConsumeRequest{"requests", 1, "bad-limits-consume-key"}); !errors.Is(e, ErrQuota) {
		t.Fatal("non-object limits must deny", e)
	}
}

func TestUpdateServicesStatusAndStoredDataValidation(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, staffUser)
	version := ""
	suspended := "suspended"
	saved, e := s.UpdateServices(admin, "ent", ServicesUpdate{ExpectedVersion: &version, Status: &suspended})
	if e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id='ent'`).Scan(&status); e != nil || status != "suspended" {
		t.Fatal(status, e)
	}
	if _, e = s.DB.Exec(`UPDATE saas_entitlements SET valid_until='not-a-date'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateServices(admin, "ent", ServicesUpdate{ExpectedVersion: &saved.Version, RenewMonths: 1}); !errors.Is(e, ErrInvalid) {
		t.Fatal("renewal over unparseable expiry", e)
	}
	if _, e = s.DB.Exec(`UPDATE saas_entitlements SET valid_until='',features_json='[]'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateServices(admin, "ent", ServicesUpdate{ExpectedVersion: &saved.Version, RenewMonths: 1}); !errors.Is(e, ErrInvalid) {
		t.Fatal("non-object features", e)
	}
}
