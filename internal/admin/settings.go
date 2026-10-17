package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/theme"
)

// commonTimezones feed the timezone datalist (any IANA name is accepted).
var commonTimezones = []string{"UTC", "Africa/Cairo", "Asia/Dubai", "Asia/Riyadh", "Asia/Amman", "Asia/Beirut", "Africa/Casablanca", "Europe/London", "Europe/Berlin", "America/New_York"}

// mediaSlot feeds the media_picker_slot partial.
type mediaSlot struct {
	Name, ID, Value   string
	Multiple, CanPick bool
}

type generalPage struct {
	EditToken      string
	Form           generalInput
	Logo           mediaSlot
	CanPickMedia   bool
	Fields         map[string]string
	Locales        []string
	Timezones      []string
	Themes         []theme.Info // nil without the theme module
	HasContent     bool
	FrontPageTitle string
}

func (h *handlers) currentGeneral(r *http.Request) generalInput {
	ctx := r.Context()
	get := func(k, def string) string { return h.d.Settings.GetString(ctx, k, def) }
	return generalInput{Title: get("site.title", ""), Tagline: get("site.tagline", ""), Locale: get("site.locale", i18n.DefaultLocale),
		Timezone: get("site.timezone", "UTC"), BaseURL: get("site.base_url", h.d.Config.BaseURL),
		Theme: get("site.theme", h.d.Config.Theme), FrontPage: get("site.front_page", ""),
		Logo: get("site.logo", ""), ContactEmail: get("contact.email", ""), PostsPerPage: strconv.Itoa(h.postsPerPage(r))}
}

// postsPerPage reads site.posts_per_page (a number; default 10).
func (h *handlers) postsPerPage(r *http.Request) int {
	var n int
	if err := h.d.Settings.Get(r.Context(), "site.posts_per_page", &n); err != nil || n < 1 || n > 100 {
		return 10
	}
	return n
}

func (h *handlers) renderGeneral(w http.ResponseWriter, r *http.Request, status int, form generalInput, fields map[string]string) {
	values, err := generalSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	token := generalEditToken(values)
	if _, ok := r.PostForm["edit_token"]; ok {
		token = r.PostForm.Get("edit_token")
	}
	if r.Method == http.MethodGet {
		form = h.generalFromSnapshot(values)
	}
	_, hasMedia := h.mediaService()
	canPick := hasMedia && auth.Can(r.Context(), "media.upload")
	data := generalPage{EditToken: token, Form: form, Fields: fields, Locales: i18n.Supported, Timezones: commonTimezones, CanPickMedia: canPick,
		Logo: mediaSlot{Name: "logo", ID: "s-logo", Value: form.Logo, CanPick: canPick}}
	if ts, ok := h.themeService(); ok {
		if list, err := ts.List(r.Context()); err == nil {
			data.Themes = list
		}
	}
	if cs, ok := h.contentService(); ok {
		data.HasContent = true
		if form.FrontPage != "" {
			if it, err := cs.Get(r.Context(), form.FrontPage); err == nil {
				data.FrontPageTitle = it.Title
			}
		}
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/general.html", data)
}

func (h *handlers) settingsGeneral(w http.ResponseWriter, r *http.Request) {
	h.renderGeneral(w, r, http.StatusOK, h.currentGeneral(r), nil)
}

func (h *handlers) settingsGeneralSave(w http.ResponseWriter, r *http.Request) {
	var in generalInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	vals, fields := h.validateGeneral(r.Context(), in)
	if len(fields) > 0 {
		h.renderGeneral(w, r, http.StatusUnprocessableEntity, in, fields)
		return
	}
	var expected *string
	if _, ok := r.PostForm["edit_token"]; ok {
		v := r.PostForm.Get("edit_token")
		expected = &v
	}
	if err := h.saveSettingsGuarded(r.Context(), vals, expected); err != nil {
		if errors.Is(err, errGeneralConflict) {
			h.renderGeneral(w, r, http.StatusConflict, in, map[string]string{"conflict": "settingscas.conflict"})
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/general")
}

type pickerPage struct {
	Items      []content.Item
	HasContent bool
}

// frontPagePicker answers the front-page content search (HTMX).
func (h *handlers) frontPagePicker(w http.ResponseWriter, r *http.Request) {
	data := pickerPage{}
	if cs, ok := h.contentService(); ok {
		data.HasContent = true
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			pg, err := cs.Query(r.Context(), content.Query{Search: q, Status: "published", PerPage: 10, Page: 1})
			if err != nil {
				h.serverError(w, r, err)
				return
			}
			data.Items = pg.Items
		}
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/system/fragments/front_page_options.html", data)
}
