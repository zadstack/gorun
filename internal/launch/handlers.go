package launch

import (
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type pageData struct {
	Brand, Locale, Dir, Title, CSRF, Error, Email, Name, Theme string
	CSS, JS                                                    string
	OtherLangHref, OtherLangLabel                              string
	LoginHref, StartHref, VerifyHref, AdminLoginHref           string
	HomeHref, AccountHref                                      string
}

func (h *handlers) base(r *http.Request, titleKey string) pageData {
	loc := i18n.Locale(r.Context())
	brand := h.brand(r)
	dir := "ltr"
	if loc == "ar" {
		dir = "rtl"
	}
	other, otherLabel := "en", h.tr(r, "launch.start.lang_en")
	if loc != "ar" {
		other, otherLabel = "ar", h.tr(r, "launch.start.lang_ar")
	}
	return pageData{
		Brand: brand, Locale: loc, Dir: dir,
		Title:          h.tr(r, titleKey, brand),
		CSRF:           auth.CSRFToken(r.Context()),
		CSS:            httpx.LaunchCSSPath,
		JS:             httpx.LaunchPolishPath,
		OtherLangHref:  pathOnly(r) + "?hl=" + other,
		OtherLangLabel: otherLabel,
		LoginHref:      h.path(r, "/start/login"),
		StartHref:      h.path(r, "/start"),
		VerifyHref:     h.path(r, "/start/verify"),
		AdminLoginHref: "/admin/login",
		HomeHref:       "/",
		AccountHref:    "/account",
	}
}

func pathOnly(r *http.Request) string {
	p := r.URL.Path
	if p == "" {
		return "/start"
	}
	return p
}

func (h *handlers) render(w http.ResponseWriter, r *http.Request, name string, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.tpl.ExecuteTemplate(w, name, data); err != nil {
		h.d.Log.Error("launch: render", "err", err, "template", name)
	}
}

func (h *handlers) users() auth.Service {
	svc, ok := kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	if !ok {
		panic("launch: auth service missing")
	}
	return svc
}

func (h *handlers) startGet(w http.ResponseWriter, r *http.Request) {
	if n, err := h.users().Count(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		if u.Role == auth.RoleCustomer {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	data := h.base(r, "launch.start.title")
	data.Theme = "business"
	h.render(w, r, "start.html", http.StatusOK, data)
}

func (h *handlers) startPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	email := strings.TrimSpace(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	theme := strings.TrimSpace(r.PostForm.Get("template"))
	data := h.base(r, "launch.start.title")
	data.Name, data.Email, data.Theme = name, email, theme
	if data.Theme == "" {
		data.Theme = "business"
	}
	switch {
	case name == "":
		data.Error = h.tr(r, "launch.error.name")
	case !validEmail(email):
		data.Error = h.tr(r, "launch.error.email")
	case utf8.RuneCountInString(password) < 10:
		data.Error = h.tr(r, "launch.error.password")
	case !validTheme(theme):
		data.Error = h.tr(r, "launch.error.theme")
	}
	if data.Error != "" {
		h.render(w, r, "start.html", http.StatusUnprocessableEntity, data)
		return
	}
	u, err := h.users().Create(r.Context(), email, name, password, auth.RoleCustomer)
	if errors.Is(err, auth.ErrEmailTaken) {
		data.Error = h.tr(r, "launch.error.taken")
		h.render(w, r, "start.html", http.StatusConflict, data)
		return
	}
	if err != nil {
		h.d.Log.Error("launch: create customer", "err", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	_ = h.d.Settings.Set(r.Context(), "launch.preferred_theme."+u.ID, theme)
	http.Redirect(w, r, h.path(r, "/start/verify")+"?email="+url.QueryEscape(email), http.StatusSeeOther)
}

func (h *handlers) loginGet(w http.ResponseWriter, r *http.Request) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		if u.Role == auth.RoleCustomer {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	h.render(w, r, "login.html", http.StatusOK, h.base(r, "launch.login.title"))
}

func (h *handlers) loginPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	data := h.base(r, "launch.login.title")
	data.Email = email
	u, err := h.users().CheckPassword(r.Context(), email, password)
	if err != nil {
		data.Error = h.tr(r, "launch.login.error")
		h.render(w, r, "login.html", http.StatusUnauthorized, data)
		return
	}
	if u.Role != auth.RoleCustomer {
		data.Error = h.tr(r, "launch.login.staff_only")
		h.render(w, r, "login.html", http.StatusForbidden, data)
		return
	}
	if err := auth.StartUserSession(h.d, w, r, u); err != nil {
		h.d.Log.Error("launch: session", "err", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (h *handlers) verifyGet(w http.ResponseWriter, r *http.Request) {
	data := h.base(r, "launch.verify.title")
	data.Email = strings.TrimSpace(r.URL.Query().Get("email"))
	h.render(w, r, "verify.html", http.StatusOK, data)
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && strings.Contains(s, ".")
}

func validTheme(s string) bool {
	switch s {
	case "business", "portfolio", "magazine":
		return true
	default:
		return false
	}
}
