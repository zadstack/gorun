package auth_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func securityRequest(t *testing.T, s *site, path string, form url.Values, ip string) (*http.Response, string) {
	t.Helper()
	r, _ := http.NewRequest("POST", s.srv.URL+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ip != "" {
		r.Header.Set("X-Forwarded-For", ip)
	}
	resp, e := s.c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	if e != nil {
		t.Fatal(e)
	}
	return resp, string(b)
}
func securitySignIn(t *testing.T, s *site, email string) {
	t.Helper()
	_, page := s.do("GET", "/admin/login", nil)
	r, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {email}, "password": {"correct horse battery"}})
	if r.StatusCode != 303 {
		t.Fatal("sign in", r.StatusCode)
	}
}
func TestIPSecurityNativeManagementPermissionsAndCAS(t *testing.T) {
	s := bootCfg(t, func(c *config.Config) { c.TrustedProxies = []string{"127.0.0.1/32", "::1/128", "192.0.2.0/24"} }, adminStub{})
	users, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	for _, role := range []string{"admin", "editor", "customer"} {
		if _, e := users.Create(context.Background(), role+"@example.com", role, "correct horse battery", role); e != nil {
			t.Fatal(e)
		}
	}
	_, page := s.do("GET", "/admin/login", nil)
	for _, ip := range []string{"203.0.113.8", "2001:db8::8"} {
		r, _ := securityRequest(t, s, "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"unknown@example.com"}, "password": {"NEVER_LOG_THIS"}}, ip)
		if r.StatusCode != 401 {
			t.Fatal("failure", r.StatusCode)
		}
	}
	rCustomer, _ := securityRequest(t, s, "/admin/login", url.Values{"_csrf": {csrfFrom(t, page)}, "email": {"customer@example.com"}, "password": {"correct horse battery"}}, "203.0.113.99")
	if rCustomer.StatusCode != 403 {
		t.Fatal("customer staff login", rCustomer.StatusCode)
	}
	var denied int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='user.login_failed' AND json_extract(meta_json,'$.reason')='staff_only'`).Scan(&denied); e != nil || denied != 1 {
		t.Fatal("staff rejection audit", denied, e)
	}
	var event, raw string
	if e := s.app.Deps.DB.QueryRow(`SELECT id,meta_json FROM audit_log WHERE action='user.login_failed' AND ip='203.0.113.8' ORDER BY id DESC LIMIT 1`).Scan(&event, &raw); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(raw, "trusted_proxy") || strings.Contains(raw, "NEVER_LOG_THIS") {
		t.Fatal("unsafe audit")
	}
	securitySignIn(t, s, "editor@example.com")
	if r, _ := s.do("GET", "/admin/security/login-attempts", nil); r.StatusCode != 403 {
		t.Fatal("editor access", r.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil)
	s.do("POST", "/admin/logout", url.Values{"_csrf": {csrfFrom(t, page)}})
	securitySignIn(t, s, "admin@example.com")
	r, page := s.do("GET", "/admin/security/login-attempts", nil)
	if r.StatusCode != 200 || !strings.Contains(page, "unknown@example.com") {
		t.Fatal("security page", r.StatusCode)
	}
	token := csrfFrom(t, page)
	fields := url.Values{"event_id": {event}, "hours": {"1"}}
	if r, _ := s.do("POST", "/admin/security/ip-block", fields); r.StatusCode != 403 {
		t.Fatal("missing CSRF", r.StatusCode)
	}
	fields.Set("_csrf", token)
	if r, _ := securityRequest(t, s, "/admin/security/ip-block", fields, "203.0.113.8"); r.StatusCode != 409 {
		t.Fatal("self block", r.StatusCode)
	}
	if r, _ := s.do("POST", "/admin/security/ip-block", fields); r.StatusCode != 303 {
		t.Fatal("block", r.StatusCode)
	}
	for _, path := range []string{"/admin/login", "/api/v1/me", "/auth/magic/invalid", "/webhooks/invalid", "/"} {
		req, _ := http.NewRequest("GET", s.srv.URL+path, nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.8")
		resp, e := s.c.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatal("block router", path, resp.StatusCode)
		}
	}
	var expiry string
	if e := s.app.Deps.DB.QueryRow(`SELECT expires_at FROM auth_ip_blocks WHERE ip='203.0.113.8'`).Scan(&expiry); e != nil {
		t.Fatal(e)
	}
	unblock := url.Values{"_csrf": {token}, "ip": {"203.0.113.8"}, "expires_at": {"stale"}}
	if r, _ := s.do("POST", "/admin/security/ip-unblock", unblock); r.StatusCode != 409 {
		t.Fatal("stale unblock", r.StatusCode)
	}
	unblock.Set("expires_at", expiry)
	if r, _ := s.do("POST", "/admin/security/ip-unblock", unblock); r.StatusCode != 303 {
		t.Fatal("unblock", r.StatusCode)
	}
	if r, _ := s.do("GET", "/admin/security/login-attempts", nil); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
}
