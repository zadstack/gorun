package auth

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const localeCookie = "gp_locale"

// AnonymousAPIPaths are API paths served without a key or session (the api
// module may append during Register, before serving starts).
var AnonymousAPIPaths = []string{"/api/v1/openapi.json", "/api/docs"}

// adminPublicPaths are reachable under /admin without a login.
var adminPublicPaths = []string{"/admin/login", "/admin/setup", "/admin/logout"}

// secureCookies reports whether cookies set while handling r get the
// Secure flag: the request is https (TLS or a trusted proxy's
// X-Forwarded-Proto), else GORUN_BASE_URL is https. The site.base_url
// setting never decides it (a wrong value would lock everyone out).
func secureCookies(r *http.Request, deps *kernel.Deps) bool {
	return httpx.SecureCookie(r, deps.Config.BaseURL)
}

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// SessionMiddleware loads the user from the gp_session cookie (sliding 14
// day expiry) into the context. It never rejects a request.
func SessionMiddleware(deps *kernel.Deps) func(http.Handler) http.Handler {
	store := newSessionStore(deps.DB)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(context.WithValue(r.Context(), cookieBaseURLKey{}, deps.Config.BaseURL))
			secure := func() bool { return secureCookies(r, deps) }
			c, err := r.Cookie(sessionCookie)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			sess, u, refreshed, err := store.load(r.Context(), c.Value)
			switch {
			case errors.Is(err, ErrNotFound):
				clearSessionCookie(w, secure())
			case err != nil:
				deps.Log.Error("auth: session", "err", err)
			default:
				if refreshed {
					setSessionCookie(w, c.Value, secure())
				}
				r = r.WithContext(withPrincipal(r.Context(), &principal{kind: "user", user: u, csrf: sess.CSRF, token: c.Value}))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyMiddleware authenticates API requests: "Authorization: Bearer gp_…"
// (actor kind api_key, scopes, per-key rate limit → 429), or else a valid
// session cookie (unsafe methods then need X-CSRF-Token). Anything else gets
// the 401 envelope, except AnonymousAPIPaths. Agent keys (KeyKindAgent) get
// 403 agent_key_not_allowed: they work only on the MCP endpoint.
func APIKeyMiddleware(deps *kernel.Deps) func(http.Handler) http.Handler {
	return apiKeyMiddleware(deps, false)
}

// MCPKeyMiddleware is APIKeyMiddleware for the MCP endpoint: it accepts
// both API and agent keys.
func MCPKeyMiddleware(deps *kernel.Deps) func(http.Handler) http.Handler {
	return apiKeyMiddleware(deps, true)
}

func apiKeyMiddleware(deps *kernel.Deps, allowAgent bool) func(http.Handler) http.Handler {
	keys := &keyService{db: deps.DB, now: time.Now}
	limiter := newBucketLimiter()
	sessions := SessionMiddleware(deps)
	return func(next http.Handler) http.Handler {
		withSession := sessions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principalFrom(r.Context())
			switch {
			case p != nil && p.user != nil:
				if !safeMethod(r.Method) && !validCSRF(w, r, p.csrf) {
					httpx.Error(w, http.StatusForbidden, "csrf_invalid", "missing or invalid X-CSRF-Token", nil)
					return
				}
				next.ServeHTTP(w, r)
			case slices.Contains(AnonymousAPIPaths, r.URL.Path):
				next.ServeHTTP(w, r)
			default:
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "API key or session required", nil)
			}
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				withSession.ServeHTTP(w, r)
				return
			}
			k, u, err := keys.authenticate(r.Context(), token)
			if err != nil {
				if !errors.Is(err, ErrInvalidToken) {
					deps.Log.Error("auth: api key", "err", err)
				}
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid API key", nil)
				return
			}
			if k.Kind == KeyKindAgent && !allowAgent {
				httpx.Error(w, http.StatusForbidden, "agent_key_not_allowed", "agent keys work only on the MCP endpoint", nil)
				return
			}
			if allowed, retry := limiter.allow(k.ID, k.RatePerMin); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
				httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded", nil)
				return
			}
			keys.touch(r.Context(), k)
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), &principal{kind: "api_key", user: u, key: k, scopes: k.Scopes})))
		})
	}
}

// bearerToken extracts the credentials of an "Authorization: Bearer …"
// header; the scheme is case-insensitive (RFC 9110).
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// localeMiddleware sets i18n.WithLocale from the user's preference, the
// gp_locale cookie, then Accept-Language.
func localeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pref := ""
		if u := UserFromContext(r.Context()); u != nil && u.Locale != "" {
			pref = u.Locale
		} else if c, err := r.Cookie(localeCookie); err == nil {
			pref = c.Value
		}
		next.ServeHTTP(w, r.WithContext(i18n.WithLocale(r.Context(), i18n.LocaleFromRequest(r, pref))))
	})
}

// adminGate sends anonymous visitors to the login page and refuses
// customers (403): the admin area requires a staff role.
func adminGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slices.Contains(adminPublicPaths, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		u := UserFromContext(r.Context())
		if u == nil {
			login := "/admin/login?next=" + url.QueryEscape(r.URL.RequestURI())
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", login)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, login, http.StatusSeeOther)
			return
		}
		if u.Role == RoleCustomer {
			deny(w, r, http.StatusForbidden, "forbidden", "the admin area requires a staff account")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// safeNext returns next when it is a local admin path, else "/admin".
func safeNext(next string) string {
	if strings.HasPrefix(next, "/admin") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "/admin"
}
