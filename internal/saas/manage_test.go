package saas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
)

func TestNativeConfigurationValidationReplayCASAndDeletion(t *testing.T) {
	s, a, _ := setup(t)
	admin := auth.WithUser(a, &auth.User{ID: "staff", Status: "active", Role: "admin"})
	q := AppInput{OwnerID: "alice", Name: "Modern", Slug: "modern", Status: "active", Mode: "subscription", IdempotencyKey: "native-app-create-request"}
	if _, e := s.CreateApp(a, q); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	app, e := s.CreateApp(admin, q)
	if e != nil || app.Version == "" {
		t.Fatal(app, e)
	}
	again, e := s.CreateApp(admin, q)
	if e != nil || again.ID != app.ID || !again.Replayed || again.Version != app.Version {
		t.Fatal(again, e)
	}
	q.Name = "Changed"
	if _, e = s.CreateApp(admin, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	q.Name = "Modern"
	q.ExpectedVersion = app.Version
	updated, e := s.UpdateApp(admin, app.ID, q)
	if e != nil || updated.Version == app.Version {
		t.Fatal(updated, e)
	}
	if _, e = s.UpdateApp(admin, app.ID, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	sub, e := s.CreateSubscription(admin, SubscriptionInput{AppID: app.ID, OwnerID: "bob", PlanID: "starter", Status: "active", Limits: map[string]int64{"tasks": 3}, Features: map[string]bool{"basic": true}, IdempotencyKey: "native-subscribe-request"})
	if e != nil {
		t.Fatal(e)
	}
	updateSub := SubscriptionInput{AppID: app.ID, OwnerID: "bob", PlanID: "pro", Status: "active", Limits: map[string]int64{"tasks": 9}, ExpectedVersion: sub.Version}
	changed, e := s.UpdateSubscription(admin, sub.ID, updateSub)
	if e != nil || changed.Version == sub.Version {
		t.Fatal(changed, e)
	}
	if _, e = s.UpdateSubscription(admin, sub.ID, updateSub); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.DeleteUnused(admin, "app", app.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.DeleteUnused(admin, "subscription", sub.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteUnused(admin, "app", app.ID); e != nil {
		t.Fatal(e)
	}
	q.OwnerID = "unknown"
	q.Slug = "unknown"
	q.IdempotencyKey = "unknown-owner-app-request"
	if _, e = s.CreateApp(admin, q); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	q.OwnerID = "alice"
	q.BaseURL = "https://username:password@example.test/"
	if _, e = s.CreateApp(admin, q); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestNativeAPIExistingScopesAndAdminFormCSRF(t *testing.T) {
	m := New()
	app := apitest.Boot(t, m)
	admin := apitest.NewClient(t, app, "saas.manage", "saas.read", "saas.use")
	owner := apitest.New(t, app, apitest.Options{Role: "customer", Scopes: []string{"saas.read"}})
	request := func(method, path string, body any, key string) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	q := AppInput{OwnerID: owner.User.ID, Name: "Owned Native", Slug: "owned-native", Status: "active", Mode: "subscription", IdempotencyKey: "test-native-app-creation"}
	w := request("POST", "/api/v1/saas/apps", q, admin.Token)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var envelope struct{ Data Saved }
	json.Unmarshal(w.Body.Bytes(), &envelope)
	subscription, e := m.Service.CreateSubscription(auth.WithUser(context.Background(), admin.User), SubscriptionInput{AppID: envelope.Data.ID, OwnerID: owner.User.ID, PlanID: "basic", Status: "active", Limits: map[string]int64{"tasks": 5}, IdempotencyKey: "test-native-sub-creation"})
	if e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/api/v1/saas", nil, owner.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Owned Native") {
		t.Fatalf("readscope: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/saas/entitlements/"+subscription.ID+"/consume", ConsumeRequest{"tasks", 1, "readscope-denied-consume"}, owner.Token)
	if w.Code != 403 {
		t.Fatalf("read key consumed: %d", w.Code)
	}
	w = request("POST", "/api/v1/saas/apps", q, owner.Token)
	if w.Code != 403 {
		t.Fatalf("customer managed: %d", w.Code)
	}
	token := strings.Repeat("s", 40)
	sum := sha256.Sum256([]byte(token))
	csrf := strings.Repeat("c", 40)
	now := time.Now().UTC()
	_, e = app.Deps.DB.Exec(`INSERT INTO sessions(id,user_id,csrf_token,ip,ua,expires_at,created_at) VALUES(?,?,?,'','',?,?)`, hex.EncodeToString(sum[:]), admin.User.ID, csrf, now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/admin/saas/new", nil)
	r.AddCookie(&http.Cookie{Name: "gp_session", Value: token})
	w = httptest.NewRecorder()
	app.Handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), csrf) {
		t.Fatalf("admin form: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admin-workspace") || strings.Contains(w.Body.String(), "href=\"/saas/assets.css\"") {
		t.Fatal("management form must use shared shell")
	}
	form := url.Values{"owner_id": {owner.User.ID}, "name": {"Form app"}, "slug": {"form-app"}, "request_key": {"csrf-form-app-request"}}
	post := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/admin/saas/apps", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "gp_session", Value: token})
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	if w = post(); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	form.Set("_csrf", csrf)
	if w = post(); w.Code != 303 {
		t.Fatalf("valid CSRF: %d %s", w.Code, w.Body.String())
	}
	checks := []struct {
		q string
		n int
	}{{`SELECT count(*) FROM saas_apps WHERE slug='form-app'`, 1}, {`SELECT count(*) FROM saas_configuration_requests WHERE request_key='csrf-form-app-request'`, 1}}
	for _, c := range checks {
		var n int
		app.Deps.DB.QueryRow(c.q).Scan(&n)
		if n != c.n {
			t.Fatalf("form writes %d", n)
		}
	}
}
