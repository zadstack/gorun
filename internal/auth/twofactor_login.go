package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

const (
	// SecondFactorPath is the sign-in step that asks for the TOTP or
	// recovery code after a password or magic-link check succeeded.
	SecondFactorPath = "/admin/login/2fa"

	challengeCookie       = "gp_2fa"
	challengeTTL          = 5 * time.Minute
	maxChallengeAttempts  = 5
	maxChallengeCodeBytes = 64
)

// ErrSecondFactorRequired is returned by StartUserSession when the user has
// two-factor authentication: no session was started; a pending sign-in
// cookie was set and the caller should redirect to SecondFactorPath.
var ErrSecondFactorRequired = errors.New("auth: second factor required")

// challenge is a sign-in waiting for its second factor.
type challenge struct {
	hash, userID, method, next string
}

// beginChallenge stores a pending sign-in for u (5 minutes, single use) and
// sets its cookie. method is the audit action of the eventual session.
func (h *handlers) beginChallenge(w http.ResponseWriter, r *http.Request, u *User, method, next string) error {
	token := randomToken(32)
	now := time.Now()
	_, err := h.deps.DB.ExecContext(r.Context(), `INSERT INTO login_challenges (token_hash, user_id, method, next, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, hashToken(token), u.ID, method, next, db.FormatTime(now.Add(challengeTTL)), db.FormatTime(now))
	if err != nil {
		return fmt.Errorf("auth: begin sign-in challenge: %w", err)
	}
	http.SetCookie(w, &http.Cookie{Name: challengeCookie, Value: token, Path: SecondFactorPath, MaxAge: int(challengeTTL.Seconds()),
		HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteStrictMode})
	return nil
}

func (h *handlers) clearChallengeCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: challengeCookie, Value: "", Path: SecondFactorPath, MaxAge: -1,
		HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteStrictMode})
}

// loadChallenge returns the unexpired challenge of the request's cookie.
func (h *handlers) loadChallenge(r *http.Request) (*challenge, error) {
	c, err := r.Cookie(challengeCookie)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return nil, ErrInvalidToken
	}
	ch := challenge{hash: hashToken(c.Value)}
	err = h.deps.DB.QueryRowContext(r.Context(), `SELECT user_id, method, next FROM login_challenges
		WHERE token_hash = ? AND expires_at > ? AND attempts < ?`, ch.hash, db.Now(), maxChallengeAttempts).Scan(&ch.userID, &ch.method, &ch.next)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load sign-in challenge: %w", err)
	}
	return &ch, nil
}

// consumeChallenge deletes the challenge; false means another request (or
// expiry) already used it.
func (h *handlers) consumeChallenge(ctx context.Context, ch *challenge) bool {
	res, err := h.deps.DB.ExecContext(ctx, `DELETE FROM login_challenges WHERE token_hash = ? AND expires_at > ? AND attempts < ?`,
		ch.hash, db.Now(), maxChallengeAttempts)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// failChallenge counts a wrong code; the challenge dies after
// maxChallengeAttempts, so the password must be entered again.
func (h *handlers) failChallenge(ctx context.Context, ch *challenge) {
	_, _ = h.deps.DB.ExecContext(ctx, `UPDATE login_challenges SET attempts = attempts + 1 WHERE token_hash = ?`, ch.hash)
	_, _ = h.deps.DB.ExecContext(ctx, `DELETE FROM login_challenges WHERE token_hash = ? AND attempts >= ?`, ch.hash, maxChallengeAttempts)
}

// completeLogin finishes a verified first factor: users with a second factor
// get a pending sign-in and go to SecondFactorPath; everyone else gets a
// session. It returns where to redirect.
func (h *handlers) completeLogin(w http.ResponseWriter, r *http.Request, u *User, action, next string) (string, error) {
	enabled, err := TwoFactorEnabled(r.Context(), h.deps.DB, u.ID)
	if err != nil {
		return "", err
	}
	if enabled {
		if err := h.beginChallenge(w, r, u, action, next); err != nil {
			return "", err
		}
		return SecondFactorPath, nil
	}
	if err := h.startSession(w, r, u, action); err != nil {
		return "", err
	}
	if u.onboardingRequired {
		return onboardingPath, nil
	}
	return next, nil
}

// secondFactorPage is the data for pages/auth/two-factor.html.
type secondFactorPage struct {
	Error   string
	Expired bool
}

func (h *handlers) secondFactorForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := h.loadChallenge(r); err != nil {
		h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/two-factor.html", secondFactorPage{Expired: true})
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/two-factor.html", secondFactorPage{})
}

// secondFactorSubmit checks the code under the login rate limits (per user
// and per IP), then turns the pending sign-in into a session.
func (h *handlers) secondFactorSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	render := func(status int, page secondFactorPage) {
		h.tpl.RenderWith(w, r, status, "auth", "pages/auth/two-factor.html", page)
	}
	ch, err := h.loadChallenge(r)
	if err != nil {
		if !errors.Is(err, ErrInvalidToken) {
			h.serverError(w, r, err)
			return
		}
		h.clearChallengeCookie(w, r)
		render(http.StatusBadRequest, secondFactorPage{Expired: true})
		return
	}
	ctx := r.Context()
	code := r.PostFormValue("code")
	if len(code) > maxChallengeCodeBytes {
		code = ""
	}
	if locked, err := h.secondFactorLimited(ctx, ch.userID); err != nil {
		h.serverError(w, r, err)
		return
	} else if locked {
		h.failChallenge(ctx, ch)
		h.secondFactorFailed(ctx, ch.userID, "locked")
		render(http.StatusTooManyRequests, secondFactorPage{Error: "auth.login.locked"})
		return
	}
	u, err := h.svc.Get(ctx, ch.userID)
	if err != nil || u.Status != StatusActive {
		h.consumeChallenge(ctx, ch)
		h.clearChallengeCookie(w, r)
		render(http.StatusBadRequest, secondFactorPage{Expired: true})
		return
	}
	actx := WithUser(ctx, u) // audit entries name the user
	if _, err := h.twoFactor.verify(actx, u.ID, code); err != nil {
		if !errors.Is(err, errTwoFactorCode) && !errors.Is(err, errTwoFactorNotEnabled) {
			h.serverError(w, r, err)
			return
		}
		h.failChallenge(ctx, ch)
		h.secondFactorFailed(ctx, u.ID, "invalid")
		render(http.StatusUnauthorized, secondFactorPage{Error: "twofactor.login.invalid"})
		return
	}
	if !h.consumeChallenge(ctx, ch) {
		h.clearChallengeCookie(w, r)
		render(http.StatusBadRequest, secondFactorPage{Expired: true})
		return
	}
	_ = h.svc.limiter.reset(ctx, secondFactorLimitKey(u.ID))
	h.clearChallengeCookie(w, r)
	if err := h.startSession(w, r, u, ch.method); err != nil {
		h.serverError(w, r, err)
		return
	}
	next := ch.next
	if u.onboardingRequired {
		next = onboardingPath
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func secondFactorLimitKey(userID string) string { return "2fa:" + userID }

// secondFactorLimited counts this attempt in the persistent login limiter
// (login_attempts): at most five codes per user and thirty per IP address
// within 15 minutes. The IP budget is separate from the password one, so a
// successful two-step sign-in does not use up a shared office address twice.
func (h *handlers) secondFactorLimited(ctx context.Context, userID string) (bool, error) {
	ipCount, err := h.svc.limiter.hit(ctx, "2fa-ip:"+audit.IPFromContext(ctx))
	if err != nil {
		return false, err
	}
	count, err := h.svc.limiter.hit(ctx, secondFactorLimitKey(userID))
	if err != nil {
		return false, err
	}
	return count > maxLoginFailures || ipCount > maxLoginIPAttempts, nil
}

// secondFactorFailed records user.2fa_failed (never the code).
func (h *handlers) secondFactorFailed(ctx context.Context, userID, reason string) {
	h.deps.Audit.Record(ctx, "user.2fa_failed", "user", userID, map[string]string{"reason": reason})
}

// cleanupChallenges is part of the hourly auth cleanup.
const cleanupChallengesSQL = `DELETE FROM login_challenges WHERE expires_at <= ?`
