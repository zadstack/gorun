package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestUnconfiguredPrivatePeerCannotSupplyClientIdentity(t *testing.T) {
	for _, peer := range []string{"127.0.0.1:9000", "10.0.0.5:9000", "[::1]:9000", "198.51.100.4:9000"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", "203.0.113.77")
		r.Header.Set("CF-Connecting-IP", "203.0.113.77")
		r.Header.Set("X-Forwarded-Proto", "https")
		RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if ClientIP(r) == "203.0.113.77" || RequestIsHTTPS(r) {
				t.Errorf("unconfigured peer trusted: %s", peer)
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
	}
}

func TestConfiguredProxyChainStopsAtFirstUntrustedHop(t *testing.T) {
	trust := []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32"), netip.MustParsePrefix("192.0.2.0/24")}
	for _, tc := range []struct{ header, want string }{
		{"198.51.100.77, 203.0.113.8, 192.0.2.2", "203.0.113.8"},
		{"198.51.100.77, 10.0.0.9", "10.0.0.9"},
		{"2001:db8::8, 192.0.2.2", "2001:db8::8"},
		{"garbage, 203.0.113.8", "10.0.0.5"},
		{strings.Repeat("1", 4097), "10.0.0.5"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.5:90"
		r.Header.Set("X-Forwarded-For", tc.header)
		r = r.WithContext(WithTrustedProxies(r.Context(), trust))
		if got := ClientIP(r); got != tc.want {
			t.Fatalf("%s: %s", tc.header[:min(len(tc.header), 80)], got)
		}
	}
}
