package auth

import (
	"errors"
	"html/template"
	"net/http"
	"slices"

	"github.com/zadstack/gorun/internal/httpx"
)

// twoFactorPage is the data for pages/auth/two-factor-setup.html.
type twoFactorPage struct {
	Status twoFactorStatus
	// Secret (grouped base32) and URI are set while an enrolment waits for
	// its first code. URI is built here (otpauth: is not a scheme
	// html/template allows in href by default).
	Secret string
	URI    template.URL
	// Codes are fresh recovery codes, shown once after enabling or
	// regenerating.
	Codes    []string
	Error    string
	Required bool // the user's role requires a second factor
	Recent   bool // signed in or confirmed the password within ReauthWindow
}

func (h *handlers) registerTwoFactor() {
	r := h.deps.Router.Admin
	r.Get("/account/two-factor", h.twoFactorShow)
	r.With(RequireRecentAuth).Post("/account/two-factor/start", h.twoFactorStart)
	r.Post("/account/two-factor/confirm", h.twoFactorConfirm)
	r.Post("/account/two-factor/recovery-codes", h.twoFactorRegenerate)
	r.Post("/account/two-factor/disable", h.twoFactorDisable)
}

// sessionUser is the signed-in browser user; account pages refuse API keys.
func sessionUser(r *http.Request) *User {
	p := principalFrom(r.Context())
	if p == nil || p.kind != "user" || p.user == nil || p.token == "" {
		return nil
	}
	return p.user
}

func (h *handlers) twoFactorRender(w http.ResponseWriter, r *http.Request, status int, u *User, page twoFactorPage) {
	st, err := h.twoFactor.status(r.Context(), u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page.Status = st
	page.Required = slices.Contains(TwoFactorRequiredRoles(r.Context(), h.deps), u.Role)
	page.Recent = RecentlyAuthenticated(r.Context(), ReauthWindow)
	if st.Pending && page.Codes == nil {
		secret, err := h.twoFactor.pendingSecret(r.Context(), u.ID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		page.Secret = formatTOTPSecret(secret)
		page.URI = template.URL(totpURI(secret, h.deps.Settings.GetString(r.Context(), "site.title", ""), u.Email)) //nolint:gosec // our own otpauth URI with escaped components
	}
	// Secrets and recovery codes must not be cached or kept in HTMX history.
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/auth/two-factor-setup.html", page)
}

func (h *handlers) twoFactorShow(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	h.twoFactorRender(w, r, http.StatusOK, u, twoFactorPage{})
}

// twoFactorStart creates (or replaces) a pending secret. It needs a recent
// sign-in or password confirmation.
func (h *handlers) twoFactorStart(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	if _, err := h.twoFactor.begin(r.Context(), u.ID); err != nil && !errors.Is(err, errTwoFactorEnabled) {
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, twoFactorEnrolPath, http.StatusSeeOther)
}

// twoFactorConfirm enables the pending secret with a valid code and shows
// the recovery codes once.
func (h *handlers) twoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	codes, err := h.twoFactor.confirm(r.Context(), u.ID, r.PostFormValue("code"))
	switch {
	case errors.Is(err, errTwoFactorCode):
		h.twoFactorRender(w, r, http.StatusUnprocessableEntity, u, twoFactorPage{Error: "twofactor.setup.invalid"})
		return
	case errors.Is(err, errNoPendingEnrolment):
		http.Redirect(w, r, twoFactorEnrolPath, http.StatusSeeOther)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	h.twoFactorRender(w, r, http.StatusOK, u, twoFactorPage{Codes: codes})
}

// twoFactorRegenerate replaces the recovery codes after a current code.
func (h *handlers) twoFactorRegenerate(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	if !h.checkSecondFactor(w, r, u) {
		return
	}
	codes, err := h.twoFactor.regenerate(r.Context(), u.ID)
	if errors.Is(err, errTwoFactorNotEnabled) {
		http.Redirect(w, r, twoFactorEnrolPath, http.StatusSeeOther)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.twoFactorRender(w, r, http.StatusOK, u, twoFactorPage{Codes: codes})
}

// twoFactorDisable removes the factor after the password and a current code.
// Members of a role that requires two-factor authentication cannot.
func (h *handlers) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	if slices.Contains(TwoFactorRequiredRoles(r.Context(), h.deps), u.Role) {
		h.twoFactorRender(w, r, http.StatusForbidden, u, twoFactorPage{Error: "twofactor.disable.required"})
		return
	}
	pw, err := h.svc.CheckPassword(r.Context(), u.Email, r.PostFormValue("password"))
	switch {
	case errors.Is(err, ErrLocked):
		h.twoFactorRender(w, r, http.StatusTooManyRequests, u, twoFactorPage{Error: "auth.login.locked"})
		return
	case errors.Is(err, ErrInvalidCredentials) || (err == nil && pw.ID != u.ID):
		h.twoFactorRender(w, r, http.StatusUnauthorized, u, twoFactorPage{Error: "auth.reauth.invalid"})
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	if !h.checkSecondFactor(w, r, u) {
		return
	}
	if _, err := h.twoFactor.remove(r.Context(), u.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.deps.Audit.Record(r.Context(), "user.2fa_disabled", "user", u.ID, nil)
	httpx.SetFlash(w, "success", "twofactor.disable.done")
	http.Redirect(w, r, twoFactorEnrolPath, http.StatusSeeOther)
}

// checkSecondFactor verifies the form's code (TOTP or recovery) under the
// second-factor rate limit; on failure it renders the page and returns false.
func (h *handlers) checkSecondFactor(w http.ResponseWriter, r *http.Request, u *User) bool {
	ctx := r.Context()
	locked, err := h.secondFactorLimited(ctx, u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return false
	}
	if locked {
		h.secondFactorFailed(ctx, u.ID, "locked")
		h.twoFactorRender(w, r, http.StatusTooManyRequests, u, twoFactorPage{Error: "auth.login.locked"})
		return false
	}
	code := r.PostFormValue("code")
	if len(code) > maxChallengeCodeBytes {
		code = ""
	}
	if _, err := h.twoFactor.verify(ctx, u.ID, code); err != nil {
		if !errors.Is(err, errTwoFactorCode) && !errors.Is(err, errTwoFactorNotEnabled) {
			h.serverError(w, r, err)
			return false
		}
		h.secondFactorFailed(ctx, u.ID, "invalid")
		h.twoFactorRender(w, r, http.StatusUnprocessableEntity, u, twoFactorPage{Error: "twofactor.login.invalid"})
		return false
	}
	_ = h.svc.limiter.reset(ctx, secondFactorLimitKey(u.ID))
	return true
}
