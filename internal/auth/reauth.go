package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// ReauthWindow is how long a sign-in or password confirmation counts as
// recent for RequireRecentAuth.
const ReauthWindow = 10 * time.Minute

const reauthPath = "/admin/reauth"

// now is the clock RecentlyAuthenticated reads (tests move it).
var reauthNow = time.Now

// RecentlyAuthenticated reports whether ctx is a browser session whose user
// signed in or confirmed their password within the last window. API keys,
// CLI and job contexts are never recent: they have no session.
func RecentlyAuthenticated(ctx context.Context, window time.Duration) bool {
	p := principalFrom(ctx)
	if p == nil || p.kind != "user" || p.token == "" || p.reauth == "" {
		return false
	}
	at, err := db.ParseTime(p.reauth)
	if err != nil {
		return false
	}
	age := reauthNow().Sub(at)
	return age >= -time.Minute && age <= window
}

// RequireRecentAuth guards sensitive admin routes (backup downloads): a
// session older than ReauthWindow is sent to /admin/reauth, which asks for
// the password again and then returns to the original URL.
func RequireRecentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RecentlyAuthenticated(r.Context(), ReauthWindow) {
			next.ServeHTTP(w, r)
			return
		}
		target := reauthPath + "?next=" + url.QueryEscape(r.URL.RequestURI())
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", target)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

func (h *handlers) reauthForm(w http.ResponseWriter, r *http.Request) {
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/reauth.html", authPage{Next: safeNext(r.URL.Query().Get("next"))})
}

func (h *handlers) reauthSubmit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p == nil || p.user == nil || p.token == "" {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	var f struct {
		Password string `form:"password"`
		Next     string `form:"next"`
	}
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	page := authPage{Next: safeNext(f.Next)}
	// CheckPassword applies the login rate limits and lockout.
	u, err := h.svc.CheckPassword(r.Context(), p.user.Email, f.Password)
	switch {
	case errors.Is(err, ErrLocked):
		page.Error = "auth.login.locked"
		h.tpl.RenderWith(w, r, http.StatusTooManyRequests, "auth", "pages/auth/reauth.html", page)
		return
	case errors.Is(err, ErrInvalidCredentials) || (err == nil && u.ID != p.user.ID):
		page.Error = "auth.reauth.invalid"
		h.tpl.RenderWith(w, r, http.StatusUnauthorized, "auth", "pages/auth/reauth.html", page)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	if err := h.sessions.markReauthenticated(r.Context(), p.token); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.deps.Audit.Record(r.Context(), "user.reauth", "user", p.user.ID, nil)
	http.Redirect(w, r, page.Next, http.StatusSeeOther)
}
