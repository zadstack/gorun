package auth

import (
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// handlers serves the auth and admin-shell routes.
type handlers struct {
	blockedAudit securitySamples
	deps         *kernel.Deps
	svc          *service
	sessions     *sessionStore
	tpl          *httpx.Templates
}

// secure is the Secure flag for cookies set while handling r.
func (h *handlers) secure(r *http.Request) bool { return secureCookies(r, h.deps) }

// authPage is the data for pages/auth/*.html.
type authPage struct {
	Next, Name, Email, Error, Token string
	Title, Tagline, Locale          string
	Fields                          map[string]string
	// NeedToken asks for GORUN_SETUP_TOKEN on the setup form (prod);
	// SetupBlocked replaces the form with instructions (prod, no token).
	NeedToken, SetupBlocked bool
}

// startSession logs u in: new session, cookie, last_login_at, audit + hook.
func (h *handlers) startSession(w http.ResponseWriter, r *http.Request, u *User, action string) error {
	token, _, err := h.sessions.create(r.Context(), u.ID, audit.IPFromContext(r.Context()), r.UserAgent())
	if err != nil {
		return err
	}
	setSessionCookie(w, token, h.secure(r))
	h.svc.touchLogin(r.Context(), u.ID)
	ctx := WithUser(r.Context(), u)
	h.deps.Audit.Record(ctx, action, "user", u.ID, nil)
	h.deps.Hooks.Emit(ctx, "user.login", map[string]string{"user_id": u.ID, "method": action})
	return nil
}

func (h *handlers) loginForm(w http.ResponseWriter, r *http.Request) {
	if n, err := h.svc.Count(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}
	if u := UserFromContext(r.Context()); u != nil && u.Role != RoleCustomer {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/login.html", authPage{Next: r.URL.Query().Get("next")})
}

type loginForm struct {
	Email    string `form:"email"`
	Password string `form:"password"`
	Next     string `form:"next"`
}

func (h *handlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	var f loginForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	page := authPage{Next: f.Next, Email: f.Email}
	u, err := h.svc.CheckPassword(r.Context(), f.Email, f.Password)
	switch {
	case errors.Is(err, ErrLocked):
		page.Error = "auth.login.locked"
		h.tpl.RenderWith(w, r, http.StatusTooManyRequests, "auth", "pages/auth/login.html", page)
		return
	case errors.Is(err, ErrInvalidCredentials):
		page.Error = "auth.login.invalid"
		h.tpl.RenderWith(w, r, http.StatusUnauthorized, "auth", "pages/auth/login.html", page)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	case u.Role == RoleCustomer:
		h.svc.failedLogin(r.Context(), u.ID, f.Email, "staff_only")
		page.Error = "auth.login.staff_only"
		h.tpl.RenderWith(w, r, http.StatusForbidden, "auth", "pages/auth/login.html", page)
		return
	}
	if err := h.startSession(w, r, u, "user.login"); err != nil {
		h.serverError(w, r, err)
		return
	}
	if u.onboardingRequired {
		http.Redirect(w, r, onboardingPath, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, safeNext(f.Next), http.StatusSeeOther)
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if p := principalFrom(r.Context()); p != nil && p.token != "" {
		if err := h.sessions.delete(r.Context(), p.token); err != nil {
			h.deps.Log.Error("auth: logout", "err", err)
		}
		h.deps.Audit.Record(r.Context(), "user.logout", "user", p.user.ID, nil)
	}
	clearSessionCookie(w, h.secure(r))
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// magicForm shows a confirmation page; GET never consumes the token, so link
// prefetchers (mail scanners) cannot use it up.
func (h *handlers) magicForm(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !magicLinkUsable(r.Context(), h.deps, token) {
		h.tpl.RenderWith(w, r, http.StatusBadRequest, "auth", "pages/auth/magic.html", authPage{Error: "auth.magic.invalid"})
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/magic.html", authPage{Token: token})
}

// magicSubmit consumes the token (atomic, single use) and logs the user in.
func (h *handlers) magicSubmit(w http.ResponseWriter, r *http.Request) {
	u, err := ConsumeMagicLink(r.Context(), h.deps, chi.URLParam(r, "token"))
	if err != nil {
		if !errors.Is(err, ErrInvalidToken) {
			h.deps.Log.Error("auth: magic link", "err", err)
		}
		h.tpl.RenderWith(w, r, http.StatusBadRequest, "auth", "pages/auth/magic.html", authPage{Error: "auth.magic.invalid"})
		return
	}
	if err := h.startSession(w, r, u, "user.magic_login"); err != nil {
		h.serverError(w, r, err)
		return
	}
	if u.onboardingRequired {
		http.Redirect(w, r, onboardingPath, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

type localeForm struct {
	Locale string `form:"locale"`
}

// setLocale stores the admin locale on the user and in a cookie.
func (h *handlers) setLocale(w http.ResponseWriter, r *http.Request) {
	var f localeForm
	if err := httpx.Bind(r, &f); err != nil || !slices.Contains(i18n.Supported, f.Locale) {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if u := UserFromContext(r.Context()); u != nil {
		if _, err := h.svc.Update(r.Context(), u.ID, UserPatch{Locale: &f.Locale}); err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: localeCookie, Value: f.Locale, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
	back := "/admin"
	if ref, err := urlPath(r.Referer()); err == nil {
		back = safeNext(ref)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.deps.Log.Error("auth: handler", "err", err, "path", httpx.RedactPath(r.URL.Path), "request_id", httpx.RequestIDFromContext(r.Context()))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}
