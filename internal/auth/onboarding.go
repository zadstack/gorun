package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const onboardingPath = "/admin/onboarding"

type onboardingPage struct {
	Name, Email, Title, Locale string
	Fields                     map[string]string
	Languages                  map[string]map[string]string
}

func (h *handlers) onboardingLanguages(fields map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, locale := range []string{"ar", "en"} {
		out[locale] = map[string]string{}
		for _, key := range []string{"auth.onboarding.title", "auth.onboarding.intro", "auth.field.name", "auth.field.email", "auth.onboarding.site_title", "auth.onboarding.password", "auth.onboarding.confirm", "auth.onboarding.locale", "auth.onboarding.submit", "auth.onboarding.secure_section", "auth.onboarding.site_section"} {
			out[locale][key] = h.deps.I18n.T(locale, key)
		}
		for _, key := range fields {
			out[locale][key] = h.deps.I18n.T(locale, key)
		}
	}
	return out
}

func (h *handlers) onboardingForm(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if !u.onboardingRequired {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	page := onboardingPage{Languages: h.onboardingLanguages(nil), Name: u.Name, Title: h.deps.Settings.GetString(r.Context(), "site.title", ""), Locale: i18n.LocaleFromRequest(r, u.Locale)}
	if u.Email != "admin@localhost.invalid" {
		page.Email = u.Email
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/onboarding.html", page)
}

func (h *handlers) onboardingSubmit(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if !u.onboardingRequired {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	// Only the generated administrator may configure the site's initial identity.
	if u.Role != RoleAdmin {
		deny(w, r, 403, "forbidden", "administrator required")
		return
	}
	var f struct {
		Name     string `form:"name"`
		Email    string `form:"email"`
		Title    string `form:"title"`
		Locale   string `form:"locale"`
		Password string `form:"password"`
		Confirm  string `form:"confirm_password"`
	}
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, "Bad request", 400)
		return
	}
	page := onboardingPage{Languages: h.onboardingLanguages(nil), Name: strings.TrimSpace(f.Name), Email: normalizeEmail(f.Email), Title: strings.TrimSpace(f.Title), Locale: f.Locale, Fields: map[string]string{}}
	if !validEmail(page.Email) || strings.HasSuffix(page.Email, ".invalid") {
		page.Fields["email"] = "auth.error.email_invalid"
	}
	if utf8.RuneCountInString(page.Name) > 200 {
		page.Fields["name"] = "auth.onboarding.name_long"
	}
	if page.Title == "" || utf8.RuneCountInString(page.Title) > 200 {
		page.Fields["title"] = "auth.onboarding.title_invalid"
	}
	if !slices.Contains(i18n.Supported, page.Locale) {
		page.Fields["locale"] = "auth.onboarding.locale_invalid"
	}
	if utf8.RuneCountInString(f.Password) < minPasswordLen || len(f.Password) > maxPasswordBytes {
		page.Fields["password"] = "auth.error.password_short"
	}
	if f.Password != f.Confirm {
		page.Fields["confirm_password"] = "auth.onboarding.confirm_invalid"
	}
	var previousHash string
	if err := h.deps.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, u.ID).Scan(&previousHash); err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(page.Fields) == 0 && VerifyPassword(previousHash, f.Password) {
		page.Fields["password"] = "auth.onboarding.password_different"
	}
	render := func() {
		page.Languages = h.onboardingLanguages(page.Fields)
		if slices.Contains(i18n.Supported, page.Locale) {
			r = r.WithContext(i18n.WithLocale(r.Context(), page.Locale))
		}
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "auth", "pages/auth/onboarding.html", page)
	}
	if len(page.Fields) > 0 {
		render()
		return
	}
	hash, err := HashPassword(f.Password)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	err = db.InTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(r.Context(), `UPDATE users SET email = ?, name = ?, password_hash = ?, locale = ?, onboarding_required = 0, updated_at = ? WHERE id = ? AND onboarding_required = 1 AND password_hash = ? AND status = 'active' AND role = 'admin' AND EXISTS (SELECT 1 FROM sessions WHERE sessions.id = ? AND sessions.user_id = users.id AND sessions.expires_at > ?)`, page.Email, page.Name, hash, page.Locale, db.Now(), u.ID, previousHash, hashToken(principalFrom(r.Context()).token), db.Now())
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
				return ErrEmailTaken
			}
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSetupDone
		}
		for key, value := range map[string]string{"site.title": page.Title, "site.locale": page.Locale} {
			if err := h.deps.Settings.SetTx(r.Context(), tx, key, value); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		// Revoke credentials issued before the temporary password was replaced.
		if _, err := tx.ExecContext(r.Context(), `UPDATE api_keys SET revoked_at = ? WHERE user_id = ? AND revoked_at = ''`, db.Now(), u.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM magic_links WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		h.deps.Audit.RecordTx(r.Context(), tx, "user.onboarding_complete", "user", u.ID, map[string]string{"email": page.Email, "title": page.Title, "locale": page.Locale})
		return nil
	})
	if errors.Is(err, ErrEmailTaken) {
		page.Fields["email"] = "auth.error.email_taken"
		render()
		return
	}
	if errors.Is(err, ErrSetupDone) {
		clearSessionCookie(w, h.secure(r))
		http.Redirect(w, r, "/admin/login", 303)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	fresh, err := h.svc.Get(r.Context(), u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.startSession(w, r, fresh, "user.onboarding_login"); err != nil {
		clearSessionCookie(w, h.secure(r))
		http.Redirect(w, r, "/admin/login", 303)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
