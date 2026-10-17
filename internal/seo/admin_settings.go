package seo

import (
	"database/sql"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	maxTemplateLen    = 200
	maxDescriptionLen = 500
	maxRobotsLen      = 10 << 10
)

type settingsHandlers struct {
	deps *kernel.Deps
	svc  *service
	tpl  *httpx.Templates
}

// settingsForm is the Settings → SEO form.
type settingsForm struct {
	TitleTemplate      string `form:"title_template"`
	DefaultDescription string `form:"default_description"`
	DefaultOGImage     string `form:"default_og_image"`
	RobotsTxt          string `form:"robots_txt"`
}

type settingsData struct {
	Form          settingsForm
	Fields        map[string]string
	RobotsDefault string
	CanPickMedia  bool
}

func (h *settingsHandlers) render(w http.ResponseWriter, r *http.Request, status int, f settingsForm, fields map[string]string) {
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/seo.html", settingsData{Form: f, Fields: fields,
		RobotsDefault: DefaultRobots(h.svc.base(r.Context())), CanPickMedia: auth.Can(r.Context(), "media.upload")})
}

func (h *settingsHandlers) form(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := h.deps.Settings
	f := settingsForm{
		TitleTemplate:      st.GetString(ctx, SettingTitleTemplate, DefaultTitleTemplate),
		DefaultDescription: st.GetString(ctx, SettingDefaultDescription, ""),
		DefaultOGImage:     st.GetString(ctx, SettingDefaultOGImage, ""),
		RobotsTxt:          st.GetString(ctx, SettingRobotsTxt, ""),
	}
	h.render(w, r, http.StatusOK, f, nil)
}

// SettingError is a rejected SEO setting value; Code is an i18n key.
type SettingError struct{ Key, Code string }

func (e *SettingError) Error() string { return "setting " + e.Key + ": " + e.Code }

// ValidateSetting validates and normalises one Settings → SEO value exactly
// as the admin screen does: SettingTitleTemplate (blank → default; must
// contain {title}, max 200 runes), SettingDefaultDescription (max 500
// runes), SettingDefaultOGImage (blank or an absolute http(s) URL) and
// SettingRobotsTxt (CRLF → LF, max 10 KB, no NUL). It returns the value to
// store or a *SettingError; other keys get code "seo.error.key".
func ValidateSetting(key, value string) (string, error) {
	fail := func(code string) (string, error) { return "", &SettingError{Key: key, Code: code} }
	switch key {
	case SettingTitleTemplate:
		v := strings.TrimSpace(value)
		if v == "" {
			v = DefaultTitleTemplate
		}
		if !strings.Contains(v, "{title}") || utf8.RuneCountInString(v) > maxTemplateLen {
			return fail("seo.error.template")
		}
		return v, nil
	case SettingDefaultDescription:
		v := strings.TrimSpace(value)
		if utf8.RuneCountInString(v) > maxDescriptionLen {
			return fail("seo.error.description_long")
		}
		return v, nil
	case SettingDefaultOGImage:
		v := strings.TrimSpace(value)
		if v != "" && absolute("", v) == "" {
			return fail("seo.error.image")
		}
		return v, nil
	case SettingRobotsTxt:
		v := strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n")
		if len(v) > maxRobotsLen || strings.ContainsRune(v, 0) {
			return fail("seo.error.robots_long")
		}
		return v, nil
	}
	return fail("seo.error.key")
}

func validateSettings(f *settingsForm) map[string]string {
	fields := map[string]string{}
	for _, it := range []struct {
		field, key string
		v          *string
	}{
		{"title_template", SettingTitleTemplate, &f.TitleTemplate}, {"default_description", SettingDefaultDescription, &f.DefaultDescription},
		{"default_og_image", SettingDefaultOGImage, &f.DefaultOGImage}, {"robots_txt", SettingRobotsTxt, &f.RobotsTxt},
	} {
		v, err := ValidateSetting(it.key, *it.v)
		if se, ok := err.(*SettingError); ok {
			fields[it.field] = se.Code
			continue
		}
		*it.v = v
	}
	return fields
}

func (h *settingsHandlers) save(w http.ResponseWriter, r *http.Request) {
	var f settingsForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if fields := validateSettings(&f); len(fields) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, f, fields)
		return
	}
	ctx := r.Context()
	st := h.deps.Settings
	err := db.InTx(ctx, h.deps.DB, func(tx *sql.Tx) error {
		for k, v := range map[string]string{SettingTitleTemplate: f.TitleTemplate, SettingDefaultDescription: f.DefaultDescription,
			SettingDefaultOGImage: f.DefaultOGImage, SettingRobotsTxt: f.RobotsTxt} {
			if err := st.SetTx(ctx, tx, k, v); err != nil {
				return err
			}
		}
		h.deps.Audit.RecordTx(ctx, tx, "seo.settings_update", "settings", "seo", map[string]any{
			"title_template": f.TitleTemplate, "default_og_image": f.DefaultOGImage, "robots_custom": f.RobotsTxt != ""})
		return nil
	})
	if err != nil {
		h.deps.Log.Error("seo: save settings", "err", err)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	httpx.SetFlash(w, "success", "seo.flash.saved")
	http.Redirect(w, r, "/admin/settings/seo", http.StatusSeeOther)
}
