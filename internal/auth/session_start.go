package auth

import (
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
)

// StartUserSession starts the native session after a caller has authenticated
// the user. It is not a password verifier; callers must verify credentials first.
// For a user with two-factor authentication it starts no session: it sets the
// pending sign-in cookie and returns ErrSecondFactorRequired, and the caller
// redirects to SecondFactorPath.
func StartUserSession(d *kernel.Deps, w http.ResponseWriter, r *http.Request, u *User) error {
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
		// step, which then lands on the admin dashboard.
		if err := h.beginChallenge(w, r, fresh, "user.login", "/admin"); err != nil {
			return err
		}
		return ErrSecondFactorRequired
	}
	return h.startSession(w, r, fresh, "user.login")
}
