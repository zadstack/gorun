package auth

import (
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestTemporaryIPBlockEnforcementAndExpiry(t *testing.T) {
	d := newDB(t)
	deps := testDeps(d)
	h := &handlers{deps: deps}
	for _, ip := range []string{"203.0.113.8", "2001:db8::8"} {
		if _, e := d.Exec(`INSERT INTO auth_ip_blocks(ip,expires_at,actor_id,event_id,created_at) VALUES(?,?,?,?,?)`, ip, db.FormatTime(time.Now().Add(time.Hour)), "owner", "event", db.Now()); e != nil {
			t.Fatal(e)
		}
		called := 0
		endpoint := h.securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(200) }))
		for range 20 {
			r := httptest.NewRequest("GET", "/admin/login", nil)
			r.RemoteAddr = ip
			r.Header.Set("X-Forwarded-For", "203.0.113.99")
			rec := httptest.NewRecorder()
			endpoint.ServeHTTP(rec, r)
			if rec.Code != 403 {
				t.Fatal(ip, rec.Code)
			}
		}
		if called != 0 {
			t.Fatal("blocked handler executed")
		}
		if _, e := d.Exec(`UPDATE auth_ip_blocks SET expires_at=? WHERE ip=?`, db.FormatTime(time.Now().Add(-time.Minute)), ip); e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = ip
		rec := httptest.NewRecorder()
		endpoint.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatal("expired block", rec.Code)
		}
	}
	var n int
	if e := d.QueryRow(`SELECT count(*) FROM audit_log WHERE action='security.ip_blocked'`).Scan(&n); e != nil || n > 2 || n < 1 {
		t.Fatal("unbounded blocked audit", n, e)
	}
}

func TestIPBlockRejectsUnsafeTargets(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "203.0.113.8:1234"
	r = r.WithContext(httpx.WithTrustedProxies(r.Context(), []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}))
	for _, ip := range []string{"203.0.113.8", "::ffff:203.0.113.8", "192.0.2.8", "127.0.0.1", "10.0.0.1", "::1", "fc00::1", "fe80::1", "fe80::1%en0", "0.0.0.0", "224.0.0.1", "203.0.113.0/24", "::/0", "garbage"} {
		if blockableIP(r, ip) {
			t.Fatal("unsafe block permitted", ip)
		}
	}
	for _, ip := range []string{"198.51.100.8", "2001:db8::8"} {
		if !blockableIP(r, ip) {
			t.Fatal("valid address rejected", ip)
		}
	}
}
func TestFailedLoginAuditSamplingIsBounded(t *testing.T) {
	d := newDB(t)
	s := testService(d)
	for range 1000 {
		s.failedLogin(withIP("203.0.113.9"), "", "name@example.com", "ip_locked")
	}
	var count int
	if e := d.QueryRow(`SELECT count(*) FROM audit_log WHERE action='user.login_failed'`).Scan(&count); e != nil || count != 30 {
		t.Fatal(count, e)
	}
}
