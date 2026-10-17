package auth

import (
	"github.com/zadstack/gorun/internal/kernel"
	"net/http"
)

// StartUserSession starts the native session after a caller has authenticated
// the user. It is not a password verifier; callers must verify credentials first.
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
	return h.startSession(w, r, fresh, "user.login")
}
