package commerce

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

// Password login uses the same rate-limited verifier and session contract as
// native login, so migrated customers do not depend on email delivery.
func (h *publicHandlers) accountPasswordLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	users, ok := h.svc.users()
	if !ok {
		http.Error(w, "account unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := audit.WithIP(r.Context(), httpx.ClientIP(r))
	u, err := users.CheckPassword(ctx, r.PostForm.Get("email"), r.PostForm.Get("password"))
	if err != nil {
		target := "/account?error=credentials"
		if next := accountNext(r.PostForm.Get("next")); next != "/account" {
			target += "&next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	next := accountNext(r.PostForm.Get("next"))
	// With two-factor the admin's second step returns here afterwards.
	if err = auth.StartUserSessionNext(h.svc.d, w, r, u, next); err != nil {
		if errors.Is(err, auth.ErrSecondFactorRequired) {
			http.Redirect(w, r, auth.SecondFactorPath, http.StatusSeeOther)
			return
		}
		http.Error(w, "account unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// Account return paths are deliberately a small native allowlist. Neither
// external URLs nor arbitrary local mutation endpoints are accepted.
func accountNext(raw string) string {
	switch raw {
	case "/agents", "/workspace", "/membership", "/saas":
		return raw
	default:
		return "/account"
	}
}
