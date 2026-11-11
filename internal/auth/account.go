package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const (
	accountPath     = "/admin/account"
	maxAccountName  = 200
	maxSessionsShow = 50
)

// accountPage is the data for pages/auth/account.html.
type accountPage struct {
	User                 *User
	Name, Email, Locale  string
	Locales              []string
	Fields               map[string]string // field → i18n error key
	HasPassword          bool
	Recent               bool
	TwoFactor            twoFactorStatus
	TwoFactorRequired    bool
	Sessions             []accountSession
	CanAPIKeys           bool
	OtherSessions        int
	CurrentSessionID     string
	PasswordSectionError bool
}

// accountSession is one row of the active-sessions list.
type accountSession struct {
	ID, Device, UA, IP, CreatedAt, LastSeenAt string
	Current                                   bool
}

func (h *handlers) registerAccount() {
	r := h.deps.Router.Admin
	r.Get("/account", h.accountShow)
	r.Post("/account/profile", h.accountProfile)
	r.Post("/account/email", h.accountEmail)
	r.Post("/account/password", h.accountPassword)
	r.Post("/account/sessions/revoke-others", h.accountRevokeOthers)
	r.Post("/account/sessions/{id}/revoke", h.accountRevoke)
}

// accountUser returns the signed-in session user or writes 403 (API keys and
// jobs have no account page).
func (h *handlers) accountUser(w http.ResponseWriter, r *http.Request) (*User, *principal) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return nil, nil
	}
	return u, principalFrom(r.Context())
}

func (h *handlers) accountShow(w http.ResponseWriter, r *http.Request) {
	u, _ := h.accountUser(w, r)
	if u == nil {
		return
	}
	h.accountRender(w, r, http.StatusOK, accountPage{Name: u.Name, Email: u.Email, Locale: u.Locale})
}

func (h *handlers) accountRender(w http.ResponseWriter, r *http.Request, status int, page accountPage) {
	ctx := r.Context()
	p := principalFrom(ctx)
	page.User = p.user
	page.Locales = i18n.Supported
	page.CurrentSessionID = hashToken(p.token)
	page.Recent = RecentlyAuthenticated(ctx, ReauthWindow)
	page.CanAPIKeys = Can(ctx, "api_keys.manage")
	page.TwoFactorRequired = slices.Contains(TwoFactorRequiredRoles(ctx, h.deps), p.user.Role)
	var hash string
	if err := h.deps.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, p.user.ID).Scan(&hash); err != nil {
		h.serverError(w, r, err)
		return
	}
	page.HasPassword = hash != ""
	st, err := h.twoFactor.status(ctx, p.user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page.TwoFactor = st
	if page.Sessions, err = h.accountSessions(ctx, p.user.ID, page.CurrentSessionID); err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, s := range page.Sessions {
		if !s.Current {
			page.OtherSessions++
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/auth/account.html", page)
}

// accountSessions lists the user's unexpired sessions, the current one first.
func (h *handlers) accountSessions(ctx context.Context, userID, current string) ([]accountSession, error) {
	rows, err := h.deps.DB.QueryContext(ctx, `SELECT id, ip, ua, created_at, last_seen_at FROM sessions
		WHERE user_id = ? AND expires_at > ? ORDER BY id = ? DESC, CASE WHEN last_seen_at = '' THEN created_at ELSE last_seen_at END DESC LIMIT ?`,
		userID, db.Now(), current, maxSessionsShow)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []accountSession
	for rows.Next() {
		var s accountSession
		if err := rows.Scan(&s.ID, &s.IP, &s.UA, &s.CreatedAt, &s.LastSeenAt); err != nil {
			return nil, err
		}
		if s.LastSeenAt == "" {
			s.LastSeenAt = s.CreatedAt
		}
		s.Current = s.ID == current
		s.Device = deviceSummary(s.UA)
		out = append(out, s)
	}
	return out, rows.Err()
}

// deviceSummary turns a user agent into "Browser · OS" (empty if unknown).
func deviceSummary(ua string) string {
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		browser = "curl"
	}
	os := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		os = "iOS"
	case strings.Contains(ua, "iPad"):
		os = "iPadOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	switch {
	case browser != "" && os != "":
		return browser + " · " + os
	default:
		return browser + os
	}
}

// accountProfile saves the name and admin UI language.
func (h *handlers) accountProfile(w http.ResponseWriter, r *http.Request) {
	u, _ := h.accountUser(w, r)
	if u == nil {
		return
	}
	var f struct {
		Name   string `form:"name"`
		Locale string `form:"locale"`
	}
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	page := accountPage{Name: strings.TrimSpace(f.Name), Email: u.Email, Locale: f.Locale, Fields: map[string]string{}}
	if utf8.RuneCountInString(page.Name) > maxAccountName {
		page.Fields["name"] = "auth.onboarding.name_long"
	}
	if !slices.Contains(i18n.Supported, f.Locale) {
		page.Fields["locale"] = "auth.onboarding.locale_invalid"
	}
	if len(page.Fields) > 0 {
		h.accountRender(w, r, http.StatusUnprocessableEntity, page)
		return
	}
	if _, err := h.svc.Update(r.Context(), u.ID, UserPatch{Name: &page.Name, Locale: &f.Locale}); err != nil {
		h.serverError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: localeCookie, Value: f.Locale, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: h.secure(r), SameSite: http.SameSiteLaxMode})
	httpx.SetFlash(w, "success", "account.profile.saved")
	http.Redirect(w, r, accountPath, http.StatusSeeOther)
}

// confirmPassword checks the current password (rate-limited like sign-in).
// Accounts without a password (magic-link users) instead need a recent
// sign-in. It returns an i18n error key, or "" when confirmed.
func (h *handlers) confirmPassword(r *http.Request, u *User, password string) (string, int, error) {
	var hash string
	if err := h.deps.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		return "", 0, err
	}
	if hash == "" {
		if RecentlyAuthenticated(r.Context(), ReauthWindow) {
			return "", 0, nil
		}
		return "account.error.not_recent", http.StatusUnauthorized, nil
	}
	got, err := h.svc.CheckPassword(r.Context(), u.Email, password)
	switch {
	case errors.Is(err, ErrLocked):
		return "auth.login.locked", http.StatusTooManyRequests, nil
	case errors.Is(err, ErrInvalidCredentials) || (err == nil && got.ID != u.ID):
		return "auth.reauth.invalid", http.StatusUnauthorized, nil
	case err != nil:
		return "", 0, err
	}
	return "", 0, nil
}

// accountEmail changes the email after the current password.
func (h *handlers) accountEmail(w http.ResponseWriter, r *http.Request) {
	u, _ := h.accountUser(w, r)
	if u == nil {
		return
	}
	var f struct {
		Email    string `form:"email"`
		Password string `form:"current_password"`
	}
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	email := normalizeEmail(f.Email)
	page := accountPage{Name: u.Name, Email: email, Locale: u.Locale, Fields: map[string]string{}}
	if !validEmail(email) || strings.HasSuffix(email, ".invalid") {
		page.Fields["email"] = "auth.error.email_invalid"
		h.accountRender(w, r, http.StatusUnprocessableEntity, page)
		return
	}
	if email == u.Email {
		http.Redirect(w, r, accountPath, http.StatusSeeOther)
		return
	}
	problem, status, err := h.confirmPassword(r, u, f.Password)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if problem != "" {
		page.Fields["email_password"] = problem
		h.accountRender(w, r, status, page)
		return
	}
	_, err = h.svc.Update(r.Context(), u.ID, UserPatch{Email: &email})
	var verr *ValidationError
	switch {
	case errors.Is(err, ErrEmailTaken):
		page.Fields["email"] = "auth.error.email_taken"
		h.accountRender(w, r, http.StatusConflict, page)
		return
	case errors.As(err, &verr):
		page.Fields["email"] = verr.Fields["email"]
		h.accountRender(w, r, http.StatusUnprocessableEntity, page)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	h.deps.Audit.Record(r.Context(), "user.email_changed", "user", u.ID, map[string]string{"from": u.Email, "to": email})
	httpx.SetFlash(w, "success", "account.email.saved")
	http.Redirect(w, r, accountPath, http.StatusSeeOther)
}

// accountPassword sets a new password after the current one, signs out
// every other session and revokes unused sign-in links. The current
// session stays signed in.
func (h *handlers) accountPassword(w http.ResponseWriter, r *http.Request) {
	u, p := h.accountUser(w, r)
	if u == nil {
		return
	}
	var f struct {
		Current string `form:"current_password"`
		New     string `form:"new_password"`
		Confirm string `form:"confirm_password"`
	}
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	page := accountPage{Name: u.Name, Email: u.Email, Locale: u.Locale, Fields: map[string]string{}, PasswordSectionError: true}
	if utf8.RuneCountInString(f.New) < minPasswordLen || len(f.New) > maxPasswordBytes {
		page.Fields["new_password"] = "auth.error.password_short"
	} else if f.New != f.Confirm {
		page.Fields["confirm_password"] = "auth.onboarding.confirm_invalid"
	}
	if len(page.Fields) > 0 {
		h.accountRender(w, r, http.StatusUnprocessableEntity, page)
		return
	}
	problem, status, err := h.confirmPassword(r, u, f.Current)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if problem != "" {
		page.Fields["current_password"] = problem
		h.accountRender(w, r, status, page)
		return
	}
	hash, err := HashPassword(f.New)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	var revoked int64
	err = db.InTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		now := db.Now()
		if _, err := tx.ExecContext(r.Context(), `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, now, u.ID); err != nil {
			return err
		}
		res, err := tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ? AND id != ?`, u.ID, hashToken(p.token))
		if err != nil {
			return err
		}
		revoked, _ = res.RowsAffected()
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM magic_links WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE sessions SET reauth_at = ? WHERE id = ?`, now, hashToken(p.token)); err != nil {
			return err
		}
		h.deps.Audit.RecordTx(r.Context(), tx, "user.password_changed", "user", u.ID, map[string]int64{"sessions_revoked": revoked})
		return nil
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "account.password.saved")
	http.Redirect(w, r, accountPath, http.StatusSeeOther)
}

// accountRevoke signs out one of the user's other sessions.
func (h *handlers) accountRevoke(w http.ResponseWriter, r *http.Request) {
	u, p := h.accountUser(w, r)
	if u == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if len(id) != 64 || id == hashToken(p.token) {
		http.Redirect(w, r, accountPath, http.StatusSeeOther)
		return
	}
	res, err := h.deps.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		// The audit target is the user; the session hash is not recorded.
		h.deps.Audit.Record(r.Context(), "user.session_revoked", "user", u.ID, nil)
		httpx.SetFlash(w, "success", "account.sessions.revoked")
	}
	http.Redirect(w, r, accountPath+"#sessions", http.StatusSeeOther)
}

// accountRevokeOthers signs out every session except this one.
func (h *handlers) accountRevokeOthers(w http.ResponseWriter, r *http.Request) {
	u, p := h.accountUser(w, r)
	if u == nil {
		return
	}
	res, err := h.deps.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ? AND id != ?`, u.ID, hashToken(p.token))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	n, _ := res.RowsAffected()
	h.deps.Audit.Record(r.Context(), "user.sessions_revoked", "user", u.ID, map[string]int64{"count": n})
	httpx.SetFlash(w, "success", "account.sessions.revoked_others")
	http.Redirect(w, r, accountPath+"#sessions", http.StatusSeeOther)
}
