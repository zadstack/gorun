package saas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
)

func TestServicesPreserveDataRenewAndRejectStaleReplay(t *testing.T) {
	s, owner, _ := setup(t)
	admin := auth.WithUser(owner, &auth.User{ID: "staff", Status: "active", Role: "admin"})
	_, e := s.DB.Exec(`UPDATE saas_entitlements SET valid_until='2027-01-31T12:30:00Z',features_json='{"internal_demo":true,"payment_received":false,"custom":{"value":123},"central_outbound_mail":true}'`)
	if e != nil {
		t.Fatal(e)
	}
	version := ""
	off := false
	q := ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1, CentralOutboundMail: &off}
	if _, e = s.UpdateServices(owner, "ent", q); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	saved, e := s.UpdateServices(admin, "ent", q)
	if e != nil || saved.Version == "" {
		t.Fatal(saved, e)
	}
	if _, e = s.UpdateServices(admin, "ent", q); !errors.Is(e, ErrConflict) {
		t.Fatal("renewal replay", e)
	}
	var expiry, raw, limits, plan, ownerID, app, status string
	e = s.DB.QueryRow(`SELECT valid_until,features_json,limits_json,plan_id,owner_id,app_id,status FROM saas_entitlements WHERE id='ent'`).Scan(&expiry, &raw, &limits, &plan, &ownerID, &app, &status)
	if e != nil || expiry != "2027-02-28T12:30:00Z" || limits != `{"requests":10}` || plan != "pro" || ownerID != "alice" || app != "app" || status != "active" {
		t.Fatal(expiry, limits, plan, ownerID, app, status, e)
	}
	var f map[string]any
	if e = json.Unmarshal([]byte(raw), &f); e != nil {
		t.Fatal(e)
	}
	if f["central_outbound_mail"] != false || f["payment_received"] != false || f["internal_demo"] != true || f["custom"].(map[string]any)["value"] != float64(123) {
		t.Fatal(f)
	}
}

func TestServicesValidationAndExpiredRenewal(t *testing.T) {
	s, ctx, _ := setup(t)
	ctx = auth.WithUser(ctx, &auth.User{ID: "staff", Status: "active", Role: "admin"})
	version := ""
	status := "invalid"
	for _, q := range []ServicesUpdate{{RenewMonths: 1}, {ExpectedVersion: &version}, {ExpectedVersion: &version, RenewMonths: -1}, {ExpectedVersion: &version, RenewMonths: 37}, {ExpectedVersion: &version, Status: &status}} {
		if _, e := s.UpdateServices(ctx, "ent", q); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%+v: %v", q, e)
		}
	}
	if _, e := s.DB.Exec(`UPDATE saas_entitlements SET valid_until='2020-01-01T00:00:00Z',status='suspended'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.UpdateServices(ctx, "ent", ServicesUpdate{ExpectedVersion: &version, RenewMonths: 2}); e != nil {
		t.Fatal(e)
	}
	var expiry, state string
	s.DB.QueryRow(`SELECT valid_until,status FROM saas_entitlements WHERE id='ent'`).Scan(&expiry, &state)
	if expiry != "2026-12-14T00:00:00Z" || state != "suspended" {
		t.Fatal(expiry, state)
	}
	if got := addCalendarMonths(time.Date(2028, 1, 31, 0, 0, 0, 0, time.UTC), 1).Day(); got != 29 {
		t.Fatal(got)
	}
}

func TestServicesHTTPScopesConflictAndOpenAPI(t *testing.T) {
	m := New()
	app := apitest.Boot(t, m)
	admin := apitest.NewClient(t, app, "saas.manage")
	viewer := apitest.New(t, app, apitest.Options{Role: "customer", Scopes: []string{"saas.read"}})
	ctx := auth.WithUser(context.Background(), admin.User)
	a, e := m.Service.CreateApp(ctx, AppInput{OwnerID: viewer.User.ID, Name: "Services", Slug: "services", Status: "active", Mode: "external", IdempotencyKey: "services-test-app-create"})
	if e != nil {
		t.Fatal(e)
	}
	sub, e := m.Service.CreateSubscription(ctx, SubscriptionInput{AppID: a.ID, OwnerID: viewer.User.ID, PlanID: "demo", Status: "active", IdempotencyKey: "services-test-sub-create"})
	if e != nil {
		t.Fatal(e)
	}
	send := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/saas/entitlements/"+sub.ID+"/services", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	body := `{"expected_version":"` + sub.Version + `","renew_months":1,"existing_domain_linked":true}`
	if w := send(viewer.Token, body); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(admin.Token, `{"renew_months":1}`); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(admin.Token, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(admin.Token, body); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if e := app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='saas.entitlement.services' AND target_id=?`, sub.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("audit", count, e)
	}
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var doc map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/v1/saas/entitlements/{id}/services"].(map[string]any)["patch"].(map[string]any)
	if op["responses"].(map[string]any)["409"] == nil {
		t.Fatal("missing conflict contract")
	}
}

// Quick renewal must clear time-based reminders without implicitly enabling a
// suspended service or replacing unrelated entitlement configuration.
func TestRenewalOnlyClearsReminderAndPreservesEntitlement(t *testing.T) {
	for _, tc := range []struct{ state, expiry, wantExpiry string }{
		{"due_soon", "2026-10-17T00:00:00Z", "2026-11-17T00:00:00Z"},
		{"expired", "2026-10-13T00:00:00Z", "2026-11-14T00:00:00Z"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s, owner, _ := setup(t)
			admin := auth.WithUser(owner, &auth.User{ID: "staff", Status: "active", Role: "admin"})
			features := `{"central_outbound_mail":true,"existing_domain_linked":true,"payment_received":false,"custom":{"keep":"value"}}`
			if _, err := s.DB.Exec(`UPDATE saas_entitlements SET valid_until=?,status='suspended',features_json=? WHERE id='ent'`, tc.expiry, features); err != nil {
				t.Fatal(err)
			}
			readReminder := func(want int) {
				t.Helper()
				w := httptest.NewRecorder()
				(&handlers{s}).overview(false)(w, httptest.NewRequest("GET", "/api/v1/saas?renewal_state=needs_renewal", nil).WithContext(owner))
				var response struct {
					Data Overview `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Data.Entitlements) != want {
					t.Fatalf("reminder count want %d: HTTP %d %s (%v)", want, w.Code, w.Body.String(), err)
				}
			}
			readReminder(1)
			version := ""
			request := ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1}
			saved, err := s.UpdateServices(admin, "ent", request)
			if err != nil {
				t.Fatal(err)
			}
			readReminder(0)
			// A double click using the original version must not extend again.
			if _, err := s.UpdateServices(admin, "ent", request); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale renewal: %v", err)
			}
			var expiry, status, raw, ownerID, appID, plan, limits, actualVersion string
			if err := s.DB.QueryRow(`SELECT valid_until,status,features_json,owner_id,app_id,plan_id,limits_json,version FROM saas_entitlements WHERE id='ent'`).Scan(&expiry, &status, &raw, &ownerID, &appID, &plan, &limits, &actualVersion); err != nil {
				t.Fatal(err)
			}
			var beforeFeatures, afterFeatures map[string]any
			if err := json.Unmarshal([]byte(features), &beforeFeatures); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), &afterFeatures); err != nil {
				t.Fatal(err)
			}
			if expiry != tc.wantExpiry || status != "suspended" || !reflect.DeepEqual(beforeFeatures, afterFeatures) || ownerID != "alice" || appID != "app" || plan != "pro" || limits != `{"requests":10}` || actualVersion != saved.Version {
				t.Fatalf("unexpected renewal mutation: %s %s %s %s %s %s %s %s", expiry, status, raw, ownerID, appID, plan, limits, actualVersion)
			}
			readReminder(0)
		})
	}
}
