package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type proxyTrustKey struct{}

// WithTrustedProxies is set only by the application from validated operator configuration.
func WithTrustedProxies(ctx context.Context, prefixes []netip.Prefix) context.Context {
	return context.WithValue(ctx, proxyTrustKey{}, append([]netip.Prefix(nil), prefixes...))
}
func peerAddress(r *http.Request) netip.Addr {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || ip.Zone() != "" {
		return netip.Addr{}
	}
	return ip.Unmap()
}
func TrustedProxyAddress(r *http.Request, ip netip.Addr) bool {
	prefixes, _ := r.Context().Value(proxyTrustKey{}).([]netip.Prefix)
	for _, p := range prefixes {
		if p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}
func trustedPeer(r *http.Request) bool { return TrustedProxyAddress(r, peerAddress(r)) }

// ClientIP walks the forwarded chain only through explicitly trusted hops.
// A malformed chain is ignored as a whole; provider-specific headers are never trusted.
func ClientIP(r *http.Request) string {
	peer := peerAddress(r)
	if !peer.IsValid() {
		return ""
	}
	if !trustedPeer(r) {
		return peer.String()
	}
	raw := r.Header.Get("X-Forwarded-For")
	parts := strings.Split(raw, ",")
	if raw == "" || len(raw) > 4096 || len(parts) > 32 {
		return peer.String()
	}
	ips := make([]netip.Addr, len(parts))
	for i, part := range parts {
		ip, e := netip.ParseAddr(strings.TrimSpace(part))
		if e != nil || ip.Zone() != "" {
			return peer.String()
		}
		ips[i] = ip.Unmap()
	}
	current := peer
	for i := len(ips) - 1; i >= 0 && TrustedProxyAddress(r, current); i-- {
		current = ips[i]
	}
	return current.String()
}
func RealIPWithTrustedProxies(prefixes []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		resolved := RealIP(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(WithTrustedProxies(r.Context(), prefixes))
			source := "peer"
			if trustedPeer(r) && ClientIP(r) != peerAddress(r).String() {
				source = "trusted_proxy"
			}
			resolved.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPSourceKey{}, source)))
		})
	}
}

type clientIPSourceKey struct{}

func ClientIPSource(ctx context.Context) string {
	s, _ := ctx.Value(clientIPSourceKey{}).(string)
	if s == "" {
		return "unverified"
	}
	return s
}
