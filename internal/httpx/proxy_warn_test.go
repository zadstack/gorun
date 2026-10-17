package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func warnerForTest(buf *bytes.Buffer, now *time.Time) func(http.Handler) http.Handler {
	w := newForwardWarner(slog.New(slog.NewTextHandler(buf, nil)))
	w.now = func() time.Time { return *now }
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { w.observe(r); next.ServeHTTP(rw, r) })
	}
}

func serveVia(t *testing.T, h http.Handler, peer string, headers map[string]string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = peer
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestUntrustedForwardingWarnsOnceThenRateLimited(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := RealIPWithTrustedProxies(nil)(warnerForTest(&buf, &now)(ok))

	serveVia(t, h, "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if n := strings.Count(buf.String(), "GORUN_TRUSTED_PROXIES"); n != 1 {
		t.Fatalf("first untrusted forward should warn once, got %d:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "peer=172.18.0.2") {
		t.Fatalf("warning should name the peer:\n%s", buf.String())
	}
	for _, hdr := range []string{"X-Forwarded-For", "Forwarded", "X-Forwarded-Proto"} {
		serveVia(t, h, "172.18.0.2:4000", map[string]string{hdr: "for=203.0.113.7"})
	}
	if n := strings.Count(buf.String(), "GORUN_TRUSTED_PROXIES"); n != 1 {
		t.Fatalf("repeats within the interval must be suppressed, got %d warnings", n)
	}
	now = now.Add(untrustedForwardInterval + time.Second)
	serveVia(t, h, "172.18.0.2:4000", map[string]string{"Forwarded": "for=203.0.113.7"})
	if n := strings.Count(buf.String(), "GORUN_TRUSTED_PROXIES"); n != 2 {
		t.Fatalf("after the interval one more warning is due, got %d", n)
	}
	if !strings.Contains(buf.String(), "similar_requests_since_last_warning=3") {
		t.Fatalf("second warning should count suppressed requests:\n%s", buf.String())
	}
}

func TestNoForwardingWarningForTrustedProxyOrPlainRequests(t *testing.T) {
	var buf bytes.Buffer
	now := time.Now()
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	trust := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	h := RealIPWithTrustedProxies(trust)(warnerForTest(&buf, &now)(ok))

	serveVia(t, h, "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "203.0.113.7", "X-Forwarded-Proto": "https"})
	serveVia(t, h, "198.51.100.9:4000", nil)
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning:\n%s", buf.String())
	}
}

func TestWarnUntrustedForwardingPassesRequestThrough(t *testing.T) {
	var buf bytes.Buffer
	called := false
	h := RealIPWithTrustedProxies(nil)(WarnUntrustedForwarding(slog.New(slog.NewTextHandler(&buf, nil)))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			called = true
			if ClientIP(r) != "198.51.100.9" {
				t.Errorf("client ip changed: %s", ClientIP(r))
			}
		})))
	serveVia(t, h, "198.51.100.9:4000", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if !called || !strings.Contains(buf.String(), "GORUN_TRUSTED_PROXIES") {
		t.Fatalf("called=%v log=%s", called, buf.String())
	}
}
