package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type usersListPage struct {
	Users  []auth.User
	Query  string
	Role   string
	Roles  []roleOption
	RoleOf map[string]string
	Pager  pager
	SelfID string
}

func (h *handlers) usersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	page, perPage := pageParams(q)
	uq := userQuery{Search: strings.TrimSpace(q.Get("q")), Role: q.Get("role"), Page: page, PerPage: perPage}
	list, total, err := listUsers(ctx, h.d.DB, uq)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	rs, err := roles(ctx, h.d.DB, i18n.Locale(ctx))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	roleOf := map[string]string{}
	for _, ro := range rs {
		roleOf[ro.Slug] = ro.Name
	}
	data := usersListPage{Users: list, Query: uq.Search, Role: uq.Role, Roles: rs, RoleOf: roleOf,
		Pager: newPager("/admin/users", q, page, perPage, total)}
	if u := auth.UserFromContext(ctx); u != nil {
		data.SelfID = u.ID
	}
	h.tpl.Render(w, r, "pages/users/list.html", data)
}

type userEditPage struct {
	User     *auth.User // nil when creating
	Form     userInput
	Fields   map[string]string
	Roles    []roleOption
	Locales  []string
	Statuses []string
	IsSelf   bool
	// MagicLink is a freshly created sign-in link, shown once when mail
	// is not configured (never stored in a flash cookie or the audit log).
	MagicLink string
}

func (h *handlers) renderUserForm(w http.ResponseWriter, r *http.Request, status int, data userEditPage) {
	rs, err := roles(r.Context(), h.d.DB, i18n.Locale(r.Context()))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	data.Roles, data.Locales, data.Statuses = rs, i18n.Supported, []string{auth.StatusActive, auth.StatusDisabled}
	if u := auth.UserFromContext(r.Context()); u != nil && data.User != nil {
		data.IsSelf = u.ID == data.User.ID
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/users/edit.html", data)
}

func (h *handlers) userNew(w http.ResponseWriter, r *http.Request) {
	h.renderUserForm(w, r, http.StatusOK, userEditPage{Form: userInput{Role: "editor", Locale: i18n.DefaultLocale, Status: auth.StatusActive}})
}

func (h *handlers) userCreate(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	u, fields, err := h.createUser(r.Context(), in)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(fields) > 0 {
		in.Password = ""
		h.renderUserForm(w, r, http.StatusUnprocessableEntity, userEditPage{Form: in, Fields: fields})
		return
	}
	httpx.SetFlash(w, "success", "admin.users.created_flash")
	redirect(w, r, "/admin/users/"+u.ID)
}

// loadUser fetches the {id} user or answers 404.
func (h *handlers) loadUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u, err := h.users.Get(r.Context(), urlID(r))
	if errors.Is(err, auth.ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		h.serverError(w, r, err)
		return nil, false
	}
	return u, true
}

func (h *handlers) userEdit(w http.ResponseWriter, r *http.Request) {
	u, ok := h.loadUser(w, r)
	if !ok {
		return
	}
	h.renderUserForm(w, r, http.StatusOK, userEditPage{User: u,
		Form: userInput{Name: u.Name, Email: u.Email, Role: u.Role, Locale: u.Locale, Status: u.Status}})
}

func (h *handlers) userUpdate(w http.ResponseWriter, r *http.Request) {
	cur, ok := h.loadUser(w, r)
	if !ok {
		return
	}
	var in userInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	_, fields, err := h.updateUser(r.Context(), cur, diffPatch(cur, in))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(fields) > 0 {
		in.Password = ""
		h.renderUserForm(w, r, http.StatusUnprocessableEntity, userEditPage{User: cur, Form: in, Fields: fields})
		return
	}
	httpx.SetFlash(w, "success", "admin.users.saved")
	redirect(w, r, "/admin/users/"+cur.ID)
}

func (h *handlers) userDelete(w http.ResponseWriter, r *http.Request) {
	err := h.deleteUser(r.Context(), urlID(r))
	switch {
	case errors.Is(err, errSelf), errors.Is(err, errLastAdmin):
		httpx.SetFlash(w, "error", guardKey(err))
	case errors.Is(err, auth.ErrNotFound):
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", "admin.users.deleted")
	}
	redirect(w, r, "/admin/users")
}

func (h *handlers) userMagicLink(w http.ResponseWriter, r *http.Request) {
	u, ok := h.loadUser(w, r)
	if !ok {
		return
	}
	if !h.mail.Configured(r.Context()) {
		h.showMagicLink(w, r, u)
		return
	}
	if err := h.sendMagicLink(r, u); err != nil {
		h.d.Log.Error("admin: magic link", "err", err, "user_id", u.ID)
		httpx.SetFlash(w, "error", "admin.users.magic_failed")
	} else {
		httpx.SetFlash(w, "success", "admin.users.magic_sent")
	}
	redirect(w, r, "/admin/users/"+u.ID)
}

// showMagicLink creates a sign-in link and shows it once on the user page
// (mail is not configured, so it cannot be emailed). The response is not
// cacheable and the panel opts out of the HTMX history cache.
func (h *handlers) showMagicLink(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx := r.Context()
	token, err := auth.NewMagicLink(ctx, h.d, u.ID, 0)
	if err != nil {
		h.d.Log.Error("admin: magic link", "err", err, "user_id", u.ID)
		httpx.SetFlash(w, "error", "admin.users.magic_failed")
		redirect(w, r, "/admin/users/"+u.ID)
		return
	}
	h.d.Audit.Record(ctx, "user.magic_link_shown", "user", u.ID, map[string]string{"email": u.Email})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("HX-Push-Url", "false")
	h.renderUserForm(w, r, http.StatusOK, userEditPage{User: u, MagicLink: h.baseURL(r) + "/auth/magic/" + token,
		Form: userInput{Name: u.Name, Email: u.Email, Role: u.Role, Locale: u.Locale, Status: u.Status}})
}

// sendMagicLink creates a single-use login link for u and mails it in the
// user's locale. The token is never logged in the audit trail.
func (h *handlers) sendMagicLink(r *http.Request, u *auth.User) error {
	ctx := r.Context()
	token, err := auth.NewMagicLink(ctx, h.d, u.ID, 0)
	if err != nil {
		return err
	}
	locale := u.Locale
	if !validLocale(locale) {
		locale = h.d.Settings.GetString(ctx, "site.locale", i18n.DefaultLocale)
	}
	name := u.Name
	if name == "" {
		name = u.Email
	}
	data := map[string]any{"Name": name, "Link": h.baseURL(r) + "/auth/magic/" + token,
		"SiteTitle": h.siteTitle(r), "TTLMinutes": int(auth.DefaultMagicLinkTTL.Minutes())}
	subject, html, text, err := h.mail.Render(locale, "magic_link", data)
	if err != nil {
		return err
	}
	if err := h.mail.Send(ctx, u.Email, subject, html, text); err != nil {
		return err
	}
	h.d.Audit.Record(ctx, "user.magic_link_sent", "user", u.ID, map[string]string{"email": u.Email})
	return nil
}

// baseURL is the public base URL (kernel.BaseURL).
func (h *handlers) baseURL(r *http.Request) string { return kernel.BaseURL(r.Context(), h.d) }

func (h *handlers) siteTitle(r *http.Request) string {
	return h.d.Settings.GetString(r.Context(), "site.title", h.tr(r, "core.app.name"))
}
