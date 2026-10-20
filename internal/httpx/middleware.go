package httpx

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	nonceKey
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID reuses a well-formed incoming X-Request-ID or generates a ULID,
// stores it in the context and echoes it in the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			id = db.NewID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFromContext returns the request id set by RequestID.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Recover turns a handler panic into a logged 500.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p)
					}
					log.Error("http: panic", "err", fmt.Sprint(p), "path", RedactPath(r.URL.Path),
						"request_id", RequestIDFromContext(r.Context()), "stack", string(debug.Stack()))
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusWriter records the response status for Logger.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush keeps http.Flusher available to streaming handlers (SSE, MCP).
func (s *statusWriter) Flush() {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

// Hijack keeps http.Hijacker available (e.g. WebSockets).
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

// Logger logs one line per request.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			log.Info("http", "method", r.Method, "path", RedactPath(r.URL.Path), "status", sw.status,
				"dur_ms", time.Since(start).Milliseconds(), "ip", audit.IPFromContext(r.Context()),
				"request_id", RequestIDFromContext(r.Context()))
		})
	}
}

// secretPathPrefixes are paths whose remaining segments are credentials
// (single-use tokens) and must never be logged.
var secretPathPrefixes = []string{"/auth/magic/"}

// RedactPath replaces everything after a secret prefix with "…".
// Locale-prefixed public paths (/en/auth/magic/…, /ar/auth/magic/…) are
// treated the same as the unprefixed credential URL.
func RedactPath(p string) string {
	loc, rest := i18n.SplitLocalePath(p)
	for _, prefix := range secretPathPrefixes {
		if strings.HasPrefix(rest, prefix) {
			if loc == "" {
				return prefix + "…"
			}
			return "/" + loc + prefix + "…"
		}
	}
	return p
}

// ScalarScript is the pinned Scalar API reference bundle served on
// /api/docs, and ScalarIntegrity its SRI hash. It is the only third-party
// script the built-in pages load, so the CSP allows exactly this URL on
// exactly that page.
const (
	ScalarScript    = "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.25.0"
	ScalarIntegrity = "sha384-VPR3QD7bdyIR9EHf6dRrFXz7NO0yJV7/UCVTLQ6e7a0wgzot5WaxvciVrrYOKEpU"
	apiDocsPath     = "/api/docs"
)

// SecurityHeaders sets CSP (with a per-request nonce, see Nonce),
// X-Frame-Options (SAMEORIGIN for public pages, DENY for /admin and /api), nosniff, Referrer-Policy, and HSTS when the request
// itself is https (RequestIsHTTPS: TLS, or X-Forwarded-Proto from a trusted
// proxy). It never follows a database setting, so a wrong site.base_url
// cannot pin browsers to https on a plain-http deployment.
//
// Scripts come from the site itself or carry the nonce. The admin adds
// 'unsafe-eval' because its Alpine directives are inline expressions that only
// the standard (eval-based) Alpine build evaluates. Public pages get no eval
// and no CDN: the bundled themes self-host everything (the default theme's
// Tailwind runtime included, which does not eval). publicScripts are the
// operator's extra public script sources (config.PublicScriptSources) for
// custom themes that still load a CDN or self-hosted Alpine. Inline styles
// serve Tailwind's runtime and HTMX indicators; blob: images serve upload
// previews.
func SecurityHeaders(publicScripts ...string) func(http.Handler) http.Handler {
	return SecurityHeadersWith(SecurityOptions{PublicScripts: publicScripts})
}

// SecurityOptions configure SecurityHeadersWith.
type SecurityOptions struct {
	// PublicScripts are extra public-page script sources (see SecurityHeaders).
	PublicScripts []string
	// HSTSIncludeSubdomains and HSTSPreload extend the HSTS header
	// (config.HSTS*). Both are off by default: includeSubDomains would force
	// https on every subdomain of the site's host, and preload is a
	// browser-wide commitment that is slow to undo.
	HSTSIncludeSubdomains, HSTSPreload bool
}

// HSTSValue is the Strict-Transport-Security value for o: one year, plus
// includeSubDomains and preload when enabled.
func (o SecurityOptions) HSTSValue() string {
	v := "max-age=31536000"
	if o.HSTSIncludeSubdomains || o.HSTSPreload {
		v += "; includeSubDomains"
	}
	if o.HSTSPreload {
		v += "; preload"
	}
	return v
}

// SecurityHeadersWith is SecurityHeaders with options.
func SecurityHeadersWith(o SecurityOptions) func(http.Handler) http.Handler {
	extra := strings.Join(o.PublicScripts, " ")
	hsts := o.HSTSValue()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := newNonce()
			// Public pages may be framed by the site itself (the admin's
			// site overview previews them); the admin and API never.
			frame, xfo := "'self'", "SAMEORIGIN"
			if neverFramed(r.URL.Path) {
				frame, xfo = "'none'", "DENY"
			}
			h := w.Header()
			scriptSources := "'self' 'nonce-" + nonce + "'"
			styleSources := "'self' 'unsafe-inline' https://fonts.googleapis.com"
			switch {
			case r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/"):
				h.Set("Cache-Control", "private, no-store")
				scriptSources += " 'unsafe-eval'"
			case strings.HasPrefix(r.URL.Path, "/auth/"):
				// The magic-link page renders the admin's auth layout (Alpine).
				scriptSources += " 'unsafe-eval'"
			case r.URL.Path == apiDocsPath:
				scriptSources += " " + ScalarScript
				styleSources += " https://cdn.jsdelivr.net"
			case extra != "":
				scriptSources += " " + extra
			}

			h.Set("Content-Security-Policy", "default-src 'self'; "+
				"script-src "+scriptSources+"; "+
				"style-src "+styleSources+"; "+
				"font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: blob: https:; "+
				"connect-src 'self'; frame-ancestors "+frame+"; base-uri 'self'; form-action 'self'; object-src 'none'")
			h.Set("X-Frame-Options", xfo)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if RequestIsHTTPS(r) {
				h.Set("Strict-Transport-Security", hsts)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey, nonce)))
		})
	}
}

// neverFramed is the admin and the API: no page may frame them, not even
// the site's own.
func neverFramed(path string) bool {
	for _, p := range []string{"/admin", "/api"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

// Nonce returns the CSP nonce for inline <script nonce="..."> tags.
func Nonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey).(string)
	return n
}

// RequestIsHTTPS reports whether r arrived over https: TLS on this
// connection, or X-Forwarded-Proto "https" (first value) set by a trusted
// proxy peer (the same trust rule as ClientIP).
func RequestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !trustedPeer(r) {
		return false
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// SecureCookie is the Secure flag for a cookie set while handling r: the
// request is https (RequestIsHTTPS), else the operator-configured base URL
// (GORUN_BASE_URL, never the site.base_url setting) is https.
func SecureCookie(r *http.Request, configuredBaseURL string) bool {
	return RequestIsHTTPS(r) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(configuredBaseURL)), "https://")
}

// RealIP stores ClientIP in the context (audit.WithIP) for audit, logging
// and login rate limiting.
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(audit.WithIP(r.Context(), ClientIP(r))))
	})
}
