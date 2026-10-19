package saas

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
)

var (
	staffUser    = &auth.User{ID: "staff", Status: "active", Role: "admin"}
	customerUser = &auth.User{ID: "alice", Status: "active", Role: "customer"}
)

// handlerRouter mounts the package handlers on a bare chi router with a fixed signed-in user,
// so handler status mapping is exercised without kernel CSRF/session middleware.
func handlerRouter(s *Service, u *auth.User) http.Handler {
	h := &handlers{s: s}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if u != nil {
				req = req.WithContext(auth.WithUser(req.Context(), u))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/apps", h.createApp)
	r.Patch("/apps/{id}", h.updateApp)
	r.Post("/subs", h.createSubscription)
	r.Put("/subs/{id}", h.updateSubscription)
	r.Patch("/subs/{id}/services", h.updateServices)
	r.Post("/subs/{id}/consume", h.consume)
	r.Patch("/subs/{id}", h.update)
	r.Get("/new", h.managementForm)
	r.Get("/manage-page", h.page(true))
	r.Post("/form/apps", h.appForm)
	r.Post("/form/apps/{id}", h.appUpdateForm)
	r.Post("/form/subs", h.subscriptionForm)
	r.Post("/form/subs/{id}/status", h.statusForm)
	return r
}

func serve(t *testing.T, hd http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	hd.ServeHTTP(w, r)
	return w
}

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

func savedFrom(t *testing.T, w *httptest.ResponseRecorder) Saved {
	t.Helper()
	var env struct{ Data Saved }
	if e := json.Unmarshal(w.Body.Bytes(), &env); e != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), e)
	}
	return env.Data
}

func TestJSONManagementHandlersRejectMalformedBodiesAndNonManagers(t *testing.T) {
	s, _, _ := setup(t)
	staff := handlerRouter(s, staffUser)
	customer := handlerRouter(s, customerUser)
	version := ""
	cases := []struct {
		method, path string
		body         any
	}{
		{"POST", "/apps", AppInput{OwnerID: "alice", Name: "X", Slug: "x-app", Status: "active", Mode: "subscription", IdempotencyKey: "denied-app-request-key"}},
		{"PATCH", "/apps/app", AppInput{OwnerID: "alice", Name: "X", Slug: "app", Status: "active", Mode: "subscription"}},
		{"POST", "/subs", SubscriptionInput{AppID: "app", OwnerID: "bob", PlanID: "p", Status: "active", IdempotencyKey: "denied-sub-request-key"}},
		{"PUT", "/subs/ent", SubscriptionInput{AppID: "app", OwnerID: "alice", PlanID: "p", Status: "active"}},
		{"PATCH", "/subs/ent/services", ServicesUpdate{ExpectedVersion: &version, RenewMonths: 1}},
		{"PATCH", "/subs/ent", StatusUpdate{ExpectedStatus: "active", Status: "suspended"}},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if w := serve(t, staff, c.method, c.path, "application/json", "{"); w.Code != 400 || errorCode(t, w) != "invalid_json" {
				t.Fatalf("malformed: %d %s", w.Code, w.Body.String())
			}
			if w := serve(t, staff, c.method, c.path, "application/json", `{"unknown_field":1}`); w.Code != 400 {
				t.Fatalf("unknown field: %d %s", w.Code, w.Body.String())
			}
			if w := serve(t, customer, c.method, c.path, "application/json", jsonBody(t, c.body)); w.Code != 403 || errorCode(t, w) != "forbidden" {
				t.Fatalf("customer: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if w := serve(t, customer, "POST", "/subs/ent/consume", "application/json", "{"); w.Code != 400 {
		t.Fatalf("consume malformed: %d", w.Code)
	}
	var status string
	if e := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id='ent'`).Scan(&status); e != nil || status != "active" {
		t.Fatal("rejected requests changed state", status, e)
	}
}

func TestJSONManagementHandlersPersistAndMapConflicts(t *testing.T) {
	s, _, _ := setup(t)
	staff := handlerRouter(s, staffUser)
	w := serve(t, staff, "POST", "/apps", "application/json", jsonBody(t, AppInput{OwnerID: "bob", Name: "Handler App", Slug: "handler-app", Status: "active", IdempotencyKey: "handler-app-create-key"}))
	if w.Code != 200 {
		t.Fatalf("create app: %d %s", w.Code, w.Body.String())
	}
	app := savedFrom(t, w)
	var mode string
	if e := s.DB.QueryRow(`SELECT mode FROM saas_apps WHERE id=?`, app.ID).Scan(&mode); e != nil || mode != "subscription" {
		t.Fatal("empty mode must default to subscription", mode, e)
	}
	upd := AppInput{OwnerID: "bob", Name: "Renamed", Slug: "handler-app", Status: "active", Mode: "subscription", ExpectedVersion: app.Version}
	if w = serve(t, staff, "PATCH", "/apps/"+app.ID, "application/json", jsonBody(t, upd)); w.Code != 200 {
		t.Fatalf("update app: %d %s", w.Code, w.Body.String())
	}
	if w = serve(t, staff, "PATCH", "/apps/"+app.ID, "application/json", jsonBody(t, upd)); w.Code != 409 || errorCode(t, w) != "conflict" {
		t.Fatalf("stale update app: %d %s", w.Code, w.Body.String())
	}
	sub := SubscriptionInput{AppID: app.ID, OwnerID: "bob", PlanID: "starter", Status: "active", Limits: map[string]int64{"tasks": 2}, IdempotencyKey: "handler-sub-create-key"}
	if w = serve(t, staff, "POST", "/subs", "application/json", jsonBody(t, sub)); w.Code != 200 {
		t.Fatalf("create sub: %d %s", w.Code, w.Body.String())
	}
	created := savedFrom(t, w)
	change := SubscriptionInput{AppID: app.ID, OwnerID: "bob", PlanID: "pro", Status: "active", Limits: map[string]int64{"tasks": 5}, ExpectedVersion: created.Version}
	if w = serve(t, staff, "PUT", "/subs/"+created.ID, "application/json", jsonBody(t, change)); w.Code != 200 {
		t.Fatalf("update sub: %d %s", w.Code, w.Body.String())
	}
	changed := savedFrom(t, w)
	if w = serve(t, staff, "PUT", "/subs/"+created.ID, "application/json", jsonBody(t, change)); w.Code != 409 {
		t.Fatalf("stale update sub: %d", w.Code)
	}
	if w = serve(t, staff, "PATCH", "/subs/"+created.ID+"/services", "application/json", `{"renew_months":1}`); w.Code != 422 || errorCode(t, w) != "validation_failed" {
		t.Fatalf("services without version: %d %s", w.Code, w.Body.String())
	}
	if w = serve(t, staff, "PATCH", "/subs/"+created.ID+"/services", "application/json", jsonBody(t, map[string]any{"expected_version": changed.Version, "renew_months": 2})); w.Code != 200 {
		t.Fatalf("services: %d %s", w.Code, w.Body.String())
	}
	var plan, until string
	if e := s.DB.QueryRow(`SELECT plan_id,valid_until FROM saas_entitlements WHERE id=?`, created.ID).Scan(&plan, &until); e != nil || plan != "pro" || until != "2026-12-14T00:00:00Z" {
		t.Fatal(plan, until, e)
	}
	if w = serve(t, staff, "PATCH", "/subs/"+created.ID, "application/json", jsonBody(t, StatusUpdate{ExpectedStatus: "active", Status: "suspended"})); w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	if w = serve(t, staff, "PATCH", "/subs/"+created.ID, "application/json", jsonBody(t, StatusUpdate{ExpectedStatus: "active", Status: "revoked"})); w.Code != 409 {
		t.Fatalf("stale status: %d", w.Code)
	}
}

func TestConsumeHandlerMapsQuotaInactiveAndSuccess(t *testing.T) {
	s, _, _ := setup(t)
	owner := handlerRouter(s, customerUser)
	consume := func(metric, key string) *httptest.ResponseRecorder {
		return serve(t, owner, "POST", "/subs/ent/consume", "application/json", jsonBody(t, ConsumeRequest{Metric: metric, Quantity: 1, IdempotencyKey: key}))
	}
	w := consume("requests", "handler-consume-ok-key")
	if w.Code != 200 {
		t.Fatalf("consume: %d %s", w.Code, w.Body.String())
	}
	var got struct{ Data Consumption }
	if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil || got.Data.Quantity != 1 || got.Data.Replayed {
		t.Fatal(got, e)
	}
	if w = consume("undefined-metric", "handler-consume-quota-key"); w.Code != 409 || errorCode(t, w) != "quota_exceeded" {
		t.Fatalf("quota: %d %s", w.Code, w.Body.String())
	}
	if _, e := s.DB.Exec(`UPDATE saas_entitlements SET status='suspended' WHERE id='ent'`); e != nil {
		t.Fatal(e)
	}
	if w = consume("requests", "handler-consume-inactive-key"); w.Code != 409 || errorCode(t, w) != "inactive" {
		t.Fatalf("inactive: %d %s", w.Code, w.Body.String())
	}
	var used int64
	if e := s.DB.QueryRow(`SELECT quantity FROM saas_usage_counters WHERE app_id='app'`).Scan(&used); e != nil || used != 1 {
		t.Fatal("only the accepted consumption is counted", used, e)
	}
}

func form(t *testing.T, hd http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, hd, "POST", path, "application/x-www-form-urlencoded", v.Encode())
}

func TestAdminFormHandlersValidateAndRedirect(t *testing.T) {
	s, _, _ := setup(t)
	staff := handlerRouter(s, staffUser)
	customer := handlerRouter(s, customerUser)
	for _, path := range []string{"/form/apps", "/form/apps/app", "/form/subs", "/form/subs/ent/status"} {
		if w := serve(t, staff, "POST", path, "application/x-www-form-urlencoded", "name=%zz"); w.Code != 422 {
			t.Fatalf("%s malformed form: %d", path, w.Code)
		}
		if w := serve(t, staff, "POST", path, "application/x-www-form-urlencoded", "name="+strings.Repeat("a", 17<<10)); w.Code != 422 {
			t.Fatalf("%s oversized form: %d", path, w.Code)
		}
	}
	appForm := url.Values{"owner_id": {"bob"}, "name": {"Form App"}, "slug": {"form-app"}, "request_key": {"form-app-request-key"}}
	if w := form(t, customer, "/form/apps", appForm); w.Code != 403 {
		t.Fatalf("customer app form: %d", w.Code)
	}
	if w := form(t, staff, "/form/apps", appForm); w.Code != 303 || w.Header().Get("Location") != "/admin/saas" {
		t.Fatalf("app form: %d", w.Code)
	}
	var appID, version string
	if e := s.DB.QueryRow(`SELECT id,version FROM saas_apps WHERE slug='form-app'`).Scan(&appID, &version); e != nil {
		t.Fatal(e)
	}
	appForm.Set("next", "subscription")
	if w := form(t, staff, "/form/apps", appForm); w.Code != 303 || w.Header().Get("Location") != "/admin/saas/new?app="+appID+"#subscription" {
		t.Fatal("guided create must select the actual saved application", w.Code, w.Header())
	}
	appForm.Set("next", "https://untrusted.invalid")
	if w := form(t, staff, "/form/apps", appForm); w.Code != 303 || w.Header().Get("Location") != "/admin/saas" {
		t.Fatal("form next must not be an open redirect", w.Code, w.Header())
	}
	update := url.Values{"owner_id": {"bob"}, "name": {"Form App 2"}, "slug": {"form-app"}, "status": {"disabled"}, "mode": {"subscription"}, "expected_version": {"stale"}}
	if w := form(t, staff, "/form/apps/"+appID, update); w.Code != 409 {
		t.Fatalf("stale app update form: %d", w.Code)
	}
	update.Set("expected_version", version)
	if w := form(t, staff, "/form/apps/"+appID, update); w.Code != 303 {
		t.Fatalf("app update form: %d %s", w.Code, w.Body.String())
	}
	var name, status string
	if e := s.DB.QueryRow(`SELECT name,status FROM saas_apps WHERE id=?`, appID).Scan(&name, &status); e != nil || name != "Form App 2" || status != "disabled" {
		t.Fatal(name, status, e)
	}
	sub := url.Values{"app_id": {"app"}, "owner_id": {"bob"}, "plan_id": {"basic"}, "metric": {"tasks"}, "limit": {"many"}, "request_key": {"form-sub-request-key"}}
	if w := form(t, staff, "/form/subs", sub); w.Code != 422 {
		t.Fatalf("non-numeric limit: %d", w.Code)
	}
	sub.Set("limit", "7")
	if w := form(t, customer, "/form/subs", sub); w.Code != 403 {
		t.Fatalf("customer subscription form: %d", w.Code)
	}
	if w := form(t, staff, "/form/subs", sub); w.Code != 303 {
		t.Fatalf("subscription form: %d %s", w.Code, w.Body.String())
	}
	var limits string
	if e := s.DB.QueryRow(`SELECT limits_json FROM saas_entitlements WHERE owner_id='bob' AND app_id='app'`).Scan(&limits); e != nil || limits != `{"tasks":7}` {
		t.Fatal(limits, e)
	}
	statusForm := url.Values{"expected_status": {"suspended"}, "status": {"revoked"}}
	if w := form(t, staff, "/form/subs/ent/status", statusForm); w.Code != 409 {
		t.Fatalf("stale status form: %d", w.Code)
	}
	statusForm.Set("expected_status", "active")
	if w := form(t, staff, "/form/subs/ent/status", statusForm); w.Code != 303 {
		t.Fatalf("status form: %d %s", w.Code, w.Body.String())
	}
	if e := s.DB.QueryRow(`SELECT status FROM saas_entitlements WHERE id='ent'`).Scan(&status); e != nil || status != "revoked" {
		t.Fatal(status, e)
	}
}

func TestManagementFormAndPageFailures(t *testing.T) {
	s, _, _ := setup(t)
	customer := handlerRouter(s, customerUser)
	staff := handlerRouter(s, staffUser)
	for _, path := range []string{"/new", "/manage-page"} {
		if w := serve(t, customer, "GET", path, "", ""); w.Code != 403 {
			t.Fatalf("%s customer: %d", path, w.Code)
		}
	}
	// The fixture users table has no name/email columns, so the owner query fails.
	if w := serve(t, staff, "GET", "/new", "", ""); w.Code != 500 || strings.Contains(w.Body.String(), "no such column") {
		t.Fatalf("owner query failure: %d %s", w.Code, w.Body.String())
	}
	if _, e := s.DB.Exec(`ALTER TABLE users ADD COLUMN name TEXT;ALTER TABLE users ADD COLUMN email TEXT`); e != nil {
		t.Fatal(e)
	}
	if w := serve(t, staff, "GET", "/new", "", ""); w.Code != 500 {
		t.Fatalf("NULL owner scan: %d %s", w.Code, w.Body.String())
	}
	if _, e := s.DB.Exec(`UPDATE users SET name='Owner '||id,email=id||'@example.test'`); e != nil {
		t.Fatal(e)
	}
	w := serve(t, staff, "GET", "/new", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "bob@example.test") || !strings.Contains(w.Body.String(), "Private app") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("form: %d %s", w.Code, w.Body.String())
	}
}
