package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const maxSiteTitleRunes = 200

// setupMode says how /admin/setup is guarded. In prod the first admin can
// only be created with the GORUN_SETUP_TOKEN (so whoever reaches a fresh
// deploy first cannot claim it); without a token the page only explains
// how to proceed. Dev needs no token unless one is configured.
func (h *handlers) setupMode() (needToken, blocked bool) {
	if h.deps.Config.SetupToken != "" {
		return true, false
	}
	return false, h.deps.Config.Env == "prod"
}

// setupTokenOK compares the submitted token with the configured one in
// constant time (over SHA-256 digests, so the length does not leak).
func (h *handlers) setupTokenOK(got string) bool {
	a := sha256.Sum256([]byte(strings.TrimSpace(got)))
	b := sha256.Sum256([]byte(h.deps.Config.SetupToken))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (h *handlers) setupForm(w http.ResponseWriter, r *http.Request) {
	n, err := h.svc.Count(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n > 0 {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	need, blocked := h.setupMode()
	h.tpl.RenderWith(w, r, http.StatusOK, "auth", "pages/auth/setup.html", authPage{
		NeedToken: need, SetupBlocked: blocked, Locale: i18n.LocaleFromRequest(r, i18n.DefaultLocale),
	})
}

type setupForm struct {
	Name     string `form:"name"`
	Email    string `form:"email"`
	Password string `form:"password"`
	Token    string `form:"setup_token"`
	Title    string `form:"title"`
	Tagline  string `form:"tagline"`
	Locale   string `form:"locale"`
}

func validateSetupIdentity(title, tagline, locale string) (string, string, string, map[string]string) {
	title = strings.TrimSpace(title)
	tagline = strings.TrimSpace(tagline)
	fields := map[string]string{}
	if title == "" || utf8.RuneCountInString(title) > maxSiteTitleRunes {
		fields["title"] = "auth.setup.title_invalid"
	}
	if utf8.RuneCountInString(tagline) > maxSiteTitleRunes {
		fields["tagline"] = "auth.setup.tagline_invalid"
	}
	if !slices.Contains(i18n.Supported, locale) {
		fields["locale"] = "auth.setup.locale_invalid"
	}
	return title, tagline, locale, fields
}

// setupSubmit creates the first admin (only while no user exists, and in
// prod only with the setup token), stores minimal site identity, and logs
// them in.
func (h *handlers) setupSubmit(w http.ResponseWriter, r *http.Request) {
	var f setupForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	title, tagline, locale, idFields := validateSetupIdentity(f.Title, f.Tagline, f.Locale)
	page := authPage{Name: f.Name, Email: f.Email, Title: title, Tagline: tagline, Locale: locale}
	// Once any user exists the page is closed for good: answer like the
	// GET (no token check, no audit row, no oracle for a leftover token).
	if n, err := h.svc.Count(r.Context()); err != nil {
		h.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	need, blocked := h.setupMode()
	page.NeedToken = need
	if blocked {
		h.tpl.RenderWith(w, r, http.StatusForbidden, "auth", "pages/auth/setup.html", authPage{SetupBlocked: true})
		return
	}
	if need && !h.setupTokenOK(f.Token) {
		h.deps.Audit.Record(r.Context(), "user.setup_denied", "user", "", map[string]string{"reason": "setup_token"})
		page.Fields = map[string]string{"setup_token": "auth.setup.token_invalid"}
		h.tpl.RenderWith(w, r, http.StatusForbidden, "auth", "pages/auth/setup.html", page)
		return
	}
	if len(idFields) > 0 {
		page.Fields = idFields
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "auth", "pages/auth/setup.html", page)
		return
	}
	var u *User
	err := db.InTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		created, err := h.svc.createFirstAdminTx(r.Context(), tx, f.Email, f.Name, f.Password, locale)
		if err != nil {
			return err
		}
		u = created
		for key, value := range map[string]string{"site.title": title, "site.tagline": tagline, "site.locale": locale} {
			if err := h.deps.Settings.SetTx(r.Context(), tx, key, value); err != nil {
				return err
			}
		}
		h.deps.Audit.RecordTx(r.Context(), tx, "user.create", "user", u.ID, map[string]string{"email": u.Email, "role": u.Role})
		return nil
	})
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		page.Fields = ve.Fields
		h.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "auth", "pages/auth/setup.html", page)
		return
	case errors.Is(err, ErrSetupDone):
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	if err := h.startSession(w, r, u, "user.setup"); err != nil {
		h.serverError(w, r, err)
		return
	}
	// First-run: pick Simple CMS vs Agents + Commerce before the full admin.
	http.Redirect(w, r, "/admin/workspace", http.StatusSeeOther)
}

// urlPath returns the path (+query) of an absolute or relative URL.
func urlPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery, nil
	}
	return u.Path, nil
}
