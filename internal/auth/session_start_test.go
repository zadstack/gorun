package auth_test

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartUserSessionUsesNativeCustomerSession(t *testing.T) {
	app, err := kernel.Boot(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), BaseURL: "https://example.test", SecretKey: make([]byte, 32)}, []kernel.Module{settingsmod.New(), auth.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := svc.Create(context.Background(), "customer@example.test", "Customer", "correct horse battery", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "https://example.test/start/login", nil)
	if err := auth.StartUserSession(app.Deps, w, req, u); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "gp_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie policy")
	}
	next := httptest.NewRequest("GET", "https://example.test/start", nil)
	next.AddCookie(cookie)
	authed := false
	auth.SessionMiddleware(app.Deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := auth.UserFromContext(r.Context())
		authed = got != nil && got.ID == u.ID && got.Role == auth.RoleCustomer
	})).ServeHTTP(httptest.NewRecorder(), next)
	if !authed {
		t.Fatal("native middleware cannot read session")
	}
	status := auth.StatusDisabled
	if _, err := svc.Update(context.Background(), u.ID, auth.UserPatch{Status: &status}); err != nil {
		t.Fatal(err)
	}
	if err := auth.StartUserSession(app.Deps, httptest.NewRecorder(), req, u); err == nil {
		t.Fatal("stale active user created disabled session")
	}
}
