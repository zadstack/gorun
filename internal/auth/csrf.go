package auth

import (
	"context"
	"crypto/subtle"
	"mime"
	"net/http"

	"github.com/zadstack/gorun/internal/httpx"
)

const (
	csrfCookie = "gp_csrf"
	csrfHeader = "X-CSRF-Token"
	csrfField  = "_csrf"

	maxCSRFFormBody = 1 << 20
)

type csrfKey struct{}
type cookieBaseURLKey struct{}

// CSRFToken returns the token forms must send as _csrf (or HTMX as
// X-CSRF-Token): the session's token, or for anonymous visitors the
// double-submit token from the gp_csrf cookie. Requires CSRFMiddleware.
func CSRFToken(ctx context.Context) string {
	if p := principalFrom(ctx); p != nil && p.csrf != "" {
		return p.csrf
	}
	t, _ := ctx.Value(csrfKey{}).(string)
	return t
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// CSRFMiddleware rejects non-GET/HEAD/OPTIONS requests (403) whose _csrf
// form value or X-CSRF-Token header does not match the session token. Without
// a session it uses a double-submit cookie (gp_csrf, issued on first visit),
// which protects anonymous forms such as login and setup. Run it after
// SessionMiddleware.
func CSRFMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expected := ""
			if p := principalFrom(r.Context()); p != nil && p.csrf != "" {
				expected = p.csrf
			} else {
				if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
					expected = c.Value
				} else if safeMethod(r.Method) {
					expected = randomToken(32)
					baseURL, _ := r.Context().Value(cookieBaseURLKey{}).(string)
					http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: expected, Path: "/",
						HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: httpx.SecureCookie(r, baseURL)})
				}
				r = r.WithContext(context.WithValue(r.Context(), csrfKey{}, expected))
			}
			if !safeMethod(r.Method) && !validCSRF(w, r, expected) {
				deny(w, r, http.StatusForbidden, "csrf_invalid", "missing or invalid CSRF token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// validCSRF compares the X-CSRF-Token header, or else the _csrf field of a
// URL-encoded body (read through a 1 MiB cap), with expected. Multipart
// bodies are never parsed here: they must carry the header (HTMX sends it via
// hx-headers on <body>).
func validCSRF(w http.ResponseWriter, r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	got := r.Header.Get(csrfHeader)
	if got == "" {
		ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if ct != "application/x-www-form-urlencoded" {
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCSRFFormBody)
		if err := r.ParseForm(); err != nil {
			return false
		}
		got = r.PostForm.Get(csrfField)
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
