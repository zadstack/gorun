package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// StartUserSession starts the native session after a caller has authenticated
// the user. It is not a password verifier; callers must verify credentials first.
// For a user with two-factor authentication it starts no session: it sets the
// pending sign-in cookie and returns ErrSecondFactorRequired, and the caller
// redirects to SecondFactorPath. After the second step the user lands on the
// admin dashboard; use StartUserSessionNext to return elsewhere.
func StartUserSession(d *kernel.Deps, w http.ResponseWriter, r *http.Request, u *User) error {
	return StartUserSessionNext(d, w, r, u, defaultSecondFactorNext)
}

// defaultSecondFactorNext is where a second step lands without a usable
// return path.
const defaultSecondFactorNext = "/admin"

// StartUserSessionNext is StartUserSession for a form outside the admin (the
// shop account, the launch login): when the second factor is needed, the
// second step redirects to next afterwards. next must be a same-site
// relative path ("/account?x=1"); anything else (absolute or
// protocol-relative URLs, backslashes, control characters) falls back to
// /admin, so the pending sign-in cannot become an open redirect.
func StartUserSessionNext(d *kernel.Deps, w http.ResponseWriter, r *http.Request, u *User, next string) error {
	if u == nil {
		return ErrInvalidCredentials
	}
	svc := newService(d.DB, d.Audit)
	fresh, err := svc.Get(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if fresh.Status != StatusActive {
		return ErrInvalidCredentials
	}
	h := &handlers{deps: d, svc: svc, sessions: newSessionStore(d.DB)}
	enabled, err := TwoFactorEnabled(r.Context(), d.DB, fresh.ID)
	if err != nil {
		return err
	}
	if enabled {
		// Never a session from one factor: hand over to the admin second
		// step, which then returns to next.
		if err := h.beginChallenge(w, r, fresh, "user.login", localReturnPath(next)); err != nil {
			return err
		}
		return ErrSecondFactorRequired
	}
	return h.startSession(w, r, fresh, "user.login")
}

// localReturnPath returns next when it is a same-site relative path, else
// defaultSecondFactorNext.
func localReturnPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\") {
		return defaultSecondFactorNext
	}
	// url.Parse refuses raw control characters.
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return defaultSecondFactorNext
	}
	// Decoded control characters ("/%0d%0a…") never reach a Location header.
	if strings.ContainsFunc(u.Path, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
		return defaultSecondFactorNext
	}
	return next
}
