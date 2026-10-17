package httpx

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

const (
	safeClientTimeout = 30 * time.Second
	maxRedirects      = 5
)

// ErrRedirectRefused is returned by a client built with NoRedirects when the
// server answers with a redirect.
var ErrRedirectRefused = errors.New("httpx: redirect refused")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// specialRanges are blocked besides the net.IP predicates: CGNAT, IETF
// protocol assignments, benchmarking, reserved (incl. broadcast), and the
// IPv6 translation prefixes (NAT64, 6to4) that can embed a private IPv4.
var specialRanges = []*net.IPNet{
	mustCIDR("100.64.0.0/10"),
	mustCIDR("192.0.0.0/24"),
	mustCIDR("198.18.0.0/15"),
	mustCIDR("240.0.0.0/4"),
	mustCIDR("64:ff9b::/96"),
	mustCIDR("2002::/16"),
}

// isBlockedIP reports addresses an outbound fetch must never reach:
// loopback, private (incl. IPv6 ULA), link-local (incl. cloud metadata
// 169.254.169.254), unspecified, multicast and the specialRanges.
func isBlockedIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || (ip.To4() != nil && ip.To4()[0] == 0) {
		return true
	}
	for _, n := range specialRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// SafeClientOptions configures NewSafeClientOpts. Zero values mean the
// defaults: private addresses blocked, same-host redirects only, 30s
// timeout, at most 5 redirects. An https request is never redirected to
// http (that would resend the body and headers in cleartext).
type SafeClientOptions struct {
	// AllowPrivate disables the private-network dial check (tests, or an
	// explicitly trusted internal endpoint).
	AllowPrivate bool
	// FollowCrossHost lets redirects leave the original host. Every hop still
	// dials through the private-network check.
	FollowCrossHost bool
	// Timeout bounds the whole request including redirects (default 30s).
	Timeout time.Duration
	// MaxRedirects caps the redirect chain (default 5).
	MaxRedirects int
	// NoRedirects refuses every redirect with ErrRedirectRefused (signed
	// webhook deliveries, which must reach exactly the configured URL).
	NoRedirects bool
}

// NewSafeClient returns an HTTP client for fetching user-supplied URLs.
// Unless allowPrivate, its dialer refuses blocked addresses after DNS
// resolution (so DNS rebinding cannot bypass it). Redirects may only stay on
// the original host. Environment proxies are ignored.
func NewSafeClient(allowPrivate bool) *http.Client {
	return NewSafeClientOpts(SafeClientOptions{AllowPrivate: allowPrivate})
}

// NewSafeClientOpts is NewSafeClient with options (see SafeClientOptions).
func NewSafeClientOpts(o SafeClientOptions) *http.Client {
	return newSafeClient(o, guardPrivate)
}

// guardPrivate is the dial guard: it refuses blocked IPs. address is the
// resolved "ip:port" being dialled.
func guardPrivate(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if isBlockedIP(net.ParseIP(host)) {
		return fmt.Errorf("httpx: connection to %s blocked (private network)", host)
	}
	return nil
}

// newSafeClient builds the client; guard runs on every dial (so on every
// redirect hop) unless o.AllowPrivate.
func newSafeClient(o SafeClientOptions, guard func(address string) error) *http.Client {
	if o.Timeout <= 0 {
		o.Timeout = safeClientTimeout
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = maxRedirects
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !o.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error { return guard(address) }
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Timeout:   o.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if o.NoRedirects {
				return fmt.Errorf("%w (to host %s)", ErrRedirectRefused, req.URL.Host)
			}
			if len(via) >= o.MaxRedirects {
				return errors.New("httpx: too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("httpx: redirect to scheme %q refused", req.URL.Scheme)
			}
			if req.URL.Scheme == "http" && via[len(via)-1].URL.Scheme == "https" {
				return errors.New("httpx: redirect from https to http refused")
			}
			if !o.FollowCrossHost && req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("httpx: redirect to another host (%s) refused", req.URL.Host)
			}
			return nil
		},
	}
}
