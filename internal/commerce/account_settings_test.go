package commerce

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAccountRegistrationSettingsPermissionsAndCSRF(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	call := func(method, csrf string) int {
		t.Helper()
		var body io.Reader
		if method == "PATCH" {
			body = strings.NewReader(`{"registration_enabled":true}`)
		}
		r, _ := http.NewRequest(method, e.srv.URL+"/api/v1/commerce/account-settings", body)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", e.srv.URL)
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := e.c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if e.svc.registrationEnabled(ctx) {
		t.Fatal("registration should default off")
	}
	if call("GET", "") != 401 {
		t.Fatal("anonymous read allowed")
	}
	_, customer, err := e.svc.ensureCustomer(ctx, "customer@example.test")
	if err != nil {
		t.Fatal(err)
	}
	e.signInCustomer(customer.ID)
	if call("GET", "") != 403 {
		t.Fatal("customer settings access")
	}
	e.signInAdmin("manager@example.test", "admin")
	csrf := e.csrfFrom("/admin/commerce/settings")
	if call("PATCH", "") != 403 {
		t.Fatal("settings write bypassed CSRF")
	}
	if call("PATCH", csrf) != 200 || !e.svc.registrationEnabled(ctx) {
		t.Fatal("authorized registration configuration failed")
	}
	response, body := e.do("GET", "/admin/commerce/settings", nil)
	if response.StatusCode != 200 || !strings.Contains(body, `name="registration_enabled" value="true" checked`) {
		t.Fatal("admin registration checkbox missing")
	}
	e.c = newClient()
	response, body = e.do("GET", "/account", nil)
	if response.StatusCode != 200 || !strings.Contains(body, `action="/account/register"`) {
		t.Fatal("public registration form absent")
	}
	if len(e.mail.all()) != 0 || len(e.stripe.reqs) != 0 {
		t.Fatal("configuration sent mail or payment")
	}
}
