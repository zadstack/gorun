package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
)

func TestRequestIDAndRecover(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := RequestID(Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Header().Get("X-Request-ID") == "" || !strings.Contains(logs.String(), "boom") {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("X-Request-ID"), logs.String())
	}
	rec = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", "abc-123")
	RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, RequestIDFromContext(r.Context())) })).ServeHTTP(rec, r)
	if rec.Body.String() != "abc-123" {
		t.Fatal(rec.Body.String())
	}
}

func TestAdminAndAPINeverFramed(t *testing.T) {
	for path, want := range map[string]string{"/admin": "DENY", "/admin/content/post": "DENY", "/api/v1/items": "DENY",
		"/": "SAMEORIGIN", "/blog/hello": "SAMEORIGIN", "/administrator-notes": "SAMEORIGIN", "/apidocs": "SAMEORIGIN"} {
		rec := httptest.NewRecorder()
		SecurityHeaders()(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		frame := "'self'"
		if want == "DENY" {
			frame = "'none'"
		}
		if rec.Header().Get("X-Frame-Options") != want || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors "+frame) {
			t.Errorf("%s: %s / %s", path, rec.Header().Get("X-Frame-Options"), rec.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestSecurityHeadersAndNonce(t *testing.T) {
	h := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, Nonce(r.Context()))
	}))
	rec := httptest.NewRecorder()
	viaProxy := httptest.NewRequest("GET", "/", nil)
	viaProxy.RemoteAddr = "10.0.0.2:4000" // Traefik on the private network
	viaProxy.Header.Set("X-Forwarded-Proto", "https")
	viaProxy = viaProxy.WithContext(WithTrustedProxies(viaProxy.Context(), []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}))
	h.ServeHTTP(rec, viaProxy)
	csp := rec.Header().Get("Content-Security-Policy")
	nonce := rec.Body.String()
	if nonce == "" || !strings.Contains(csp, "'nonce-"+nonce+"'") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("csp %q nonce %q", csp, nonce)
	}
	directives := map[string]bool{}
	for _, d := range strings.Split(csp, ";") {
		directives[strings.TrimSpace(d)] = true
	}
	for _, want := range []struct{ directive, sources string }{
		{"style-src", "'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net"}, // Quill snow CSS
		{"font-src", "'self' https://fonts.gstatic.com data:"},
		{"img-src", "'self' data: blob: https:"}, // media thumbnails/previews, Scalar
	} {
		if !directives[want.directive+" "+want.sources] {
			t.Errorf("csp lacks %q %q: %s", want.directive, want.sources, csp)
		}
	}
	for k, v := range map[string]string{"X-Frame-Options": "SAMEORIGIN", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "strict-origin-when-cross-origin"} {
		if rec.Header().Get(k) != v {
			t.Errorf("%s=%q", k, rec.Header().Get(k))
		}
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS expected for an https request (trusted X-Forwarded-Proto)")
	}
	rec = httptest.NewRecorder()
	SecurityHeaders()(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("no HSTS over http")
	}
}

// Addendum A: https is decided by the request (TLS, or X-Forwarded-Proto
// from an explicitly configured peer only); cookies fall back to the configured
// base URL, never anything else.
func TestRequestIsHTTPSAndSecureCookie(t *testing.T) {
	plain := httptest.NewRequest("GET", "/", nil) // RemoteAddr 192.0.2.1 (public)
	tlsReq := httptest.NewRequest("GET", "https://x.example/", nil)
	spoofed := httptest.NewRequest("GET", "/", nil)
	spoofed.Header.Set("X-Forwarded-Proto", "https") // from a public peer: ignored
	proxied := httptest.NewRequest("GET", "/", nil)
	proxied.RemoteAddr = "127.0.0.1:5000"
	proxied.Header.Set("X-Forwarded-Proto", "https, http")
	proxied = proxied.WithContext(WithTrustedProxies(proxied.Context(), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}))
	proxiedHTTP := httptest.NewRequest("GET", "/", nil)
	proxiedHTTP.RemoteAddr = "172.18.0.3:5000"
	proxiedHTTP.Header.Set("X-Forwarded-Proto", "http")
	for name, c := range map[string]struct {
		r    *http.Request
		want bool
	}{"plain": {plain, false}, "tls": {tlsReq, true}, "spoofed": {spoofed, false}, "proxied https": {proxied, true}, "proxied http": {proxiedHTTP, false}} {
		if got := RequestIsHTTPS(c.r); got != c.want {
			t.Errorf("%s: RequestIsHTTPS = %v", name, got)
		}
	}
	if SecureCookie(plain, "http://localhost:8080") || !SecureCookie(plain, "https://example.org") || !SecureCookie(proxied, "") {
		t.Error("SecureCookie: request https, else configured base URL scheme")
	}
}

func TestClientIPTrustsForwardedOnlyFromConfiguredPeers(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")
	r = r.WithContext(WithTrustedProxies(r.Context(), []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32"), netip.MustParsePrefix("10.0.0.2/32")}))
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("behind proxy: %s", got)
	}
	r.RemoteAddr = "198.51.100.1:1234" // public peer: header is untrusted
	if got := ClientIP(r); got != "198.51.100.1" {
		t.Fatalf("spoofed header trusted: %s", got)
	}
	var seen string
	RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = audit.IPFromContext(r.Context()) })).ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.1" {
		t.Fatalf("RealIP did not store ip: %q", seen)
	}
}

func TestRedactPath(t *testing.T) {
	cases := map[string]string{
		"/auth/magic/abcDEF123": "/auth/magic/…",
		"/auth/magic/":          "/auth/magic/…",
		"/admin/content/01ABC":  "/admin/content/01ABC",
		"/auth/magicians/abc":   "/auth/magicians/abc",
	}
	for in, want := range cases {
		if got := RedactPath(in); got != want {
			t.Errorf("RedactPath(%q) = %q, want %q", in, got, want)
		}
	}
	var logs bytes.Buffer
	h := Logger(slog.New(slog.NewTextHandler(&logs, nil)))(http.NotFoundHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/auth/magic/supersecret", nil))
	if strings.Contains(logs.String(), "supersecret") {
		t.Fatalf("token leaked into log: %s", logs.String())
	}
}

func TestAdminResponsesNotCacheable(t *testing.T) {
	for _, path := range []string{"/admin", "/admin/login", "/admin/appearance/templates"} {
		rec := httptest.NewRecorder()
		SecurityHeaders()(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s may cache authenticated HTML", path)
		}
	}
}

func TestAdminScriptsUseOnlyEmbeddedOrigins(t *testing.T) {
	for _, path := range []string{"/admin", "/admin/login", "/admin/content/post"} {
		rec := httptest.NewRecorder()
		SecurityHeaders()(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		script := ""
		for _, directive := range strings.Split(csp, ";") {
			if strings.HasPrefix(strings.TrimSpace(directive), "script-src ") {
				script = strings.TrimSpace(directive)
			}
		}
		if script == "" || !strings.Contains(script, "'self'") || !strings.Contains(script, "'nonce-") || !strings.Contains(script, "'unsafe-eval'") || strings.Contains(script, "https:") {
			t.Fatalf("%s trusts external admin scripts: %q", path, script)
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s lost admin security headers", path)
		}
	}
	for _, path := range []string{"/", "/post/example", "/administrator-notes"} {
		rec := httptest.NewRecorder()
		SecurityHeaders()(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		for _, origin := range strings.Fields(cspScriptCDNs) {
			if !strings.Contains(csp, origin) {
				t.Fatalf("%s lost public script origin %s", path, origin)
			}
		}
		if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatalf("%s lost public frame policy", path)
		}
	}
}
