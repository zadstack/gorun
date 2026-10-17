package commerce

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestCustomerPasswordLoginAndIsolation(t *testing.T) {
	e := boot(t)
	u, err := auth.NewService(e.svc.db, nil).Create(context.Background(), "password@example.test", "Customer", "secure-customer-password", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	csrf := e.csrfFrom("/account")
	resp, _ := e.do("POST", "/account/password-login", url.Values{"_csrf": {csrf}, "email": {u.Email}, "password": {"wrong"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/account?error=credentials" {
		t.Fatal("invalid login accepted")
	}
	resp, _ = e.do("POST", "/account/password-login", url.Values{"email": {u.Email}, "password": {"secure-customer-password"}})
	if resp.StatusCode != 403 {
		t.Fatal("password login without CSRF accepted")
	}
	resp, _ = e.do("POST", "/account/password-login", url.Values{"_csrf": {csrf}, "email": {u.Email}, "password": {"secure-customer-password"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/account" {
		t.Fatalf("valid login rejected: %d", resp.StatusCode)
	}
	resp, body := e.do("GET", "/account", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, u.Email) || strings.Contains(body, `action="/account/password-login"`) {
		t.Fatal("customer session missing")
	}
	if len(e.mail.all()) != 0 || len(e.stripe.reqs) != 0 {
		t.Fatal("password login caused external side effect")
	}
}

func TestRegistrationNeverGrantsRoleOrPaidAccess(t *testing.T) {
	e := boot(t)
	csrf := e.csrfFrom("/account")
	form := url.Values{"_csrf": {csrf}, "email": {"new-customer@example.test"}, "name": {"New customer"}, "password": {"secure-registration-password"}, "role": {"admin"}}
	resp, _ := e.do("POST", "/account/register", form)
	if resp.StatusCode != 404 {
		t.Fatal("registration must be opt in")
	}
	if err := e.svc.d.Settings.Set(context.Background(), "commerce.registration_enabled", true); err != nil {
		t.Fatal(err)
	}
	resp, _ = e.do("POST", "/account/register", form)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/account" {
		t.Fatalf("registration failed %d", resp.StatusCode)
	}
	u, err := auth.NewService(e.svc.db, nil).GetByEmail(context.Background(), "new-customer@example.test")
	if err != nil || u.Role != auth.RoleCustomer {
		t.Fatal("untrusted role accepted")
	}
	if e.count("SELECT count(*) FROM entitlements") != 0 || e.count("SELECT count(*) FROM orders") != 0 || len(e.mail.all()) != 0 {
		t.Fatal("registration granted paid access or sent mail")
	}
}

func TestPasswordLoginNativeReturnPath(t *testing.T) {
	e := boot(t)
	u, err := auth.NewService(e.svc.db, nil).Create(context.Background(), "return@example.test", "Customer", "secure-customer-password", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	_, page := e.do("GET", "/account?next=/agents", nil)
	if !strings.Contains(page, `name="next" value="/agents"`) || !strings.Contains(page, `action="/account/password-login"`) {
		t.Fatal("password login lost return destination")
	}
	csrf := e.csrfFrom("/account?next=/agents")
	form := url.Values{"_csrf": {csrf}, "email": {u.Email}, "password": {"wrong"}, "next": {"/agents"}}
	resp, _ := e.do("POST", "/account/password-login", form)
	if resp.Header.Get("Location") != "/account?error=credentials&next=%2Fagents" {
		t.Fatal("retry lost return path")
	}
	form.Set("password", "secure-customer-password")
	resp, _ = e.do("POST", "/account/password-login", form)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/agents" {
		t.Fatal("password login did not return to agents")
	}
	if len(e.mail.all()) != 0 {
		t.Fatal("password flow requires mail")
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", "/\\evil.example", "/agents?next=https://evil.example", "/admin/users/delete", "%2f%2fevil.example"} {
		if accountNext(bad) != "/account" {
			t.Fatalf("unsafe next accepted %q", bad)
		}
	}
}
