package admin

import (
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/mailer"
)

var mailKeys = []string{"mail.provider", "mail.resend_api_key", "mail.password", "mail.host", "mail.port", "mail.username", "mail.from", "mail.from_name", "mail.tls"}
var tlsModes = []string{mailer.TLSStartTLS, mailer.TLSImplicit, mailer.TLSNone}

// mailInput is the Settings → Mail form. A blank Password keeps the stored
// one; ClearPassword removes it.
type mailInput struct {
	EditToken         string `form:"edit_token"`
	Provider          string `form:"provider"`
	ResendAPIKey      string `form:"resend_api_key"`
	ClearResendAPIKey bool   `form:"clear_resend_api_key"`
	Host              string `form:"host"`
	Port              string `form:"port"`
	Username          string `form:"username"`
	Password          string `form:"password"`
	ClearPassword     bool   `form:"clear_password"`
	From              string `form:"from"`
	FromName          string `form:"from_name"`
	TLS               string `form:"tls"`
}

type mailPage struct {
	APIKeySet   bool
	Form        mailInput
	Fields      map[string]string
	TLSModes    []string
	PasswordSet bool
	Configured  bool
}

func (h *handlers) currentMail(r *http.Request) (mailInput, bool, error) {
	snap, err := mailSnapshot(r.Context(), h.d.DB)
	if err != nil {
		return mailInput{}, false, err
	}
	return mailFromSnapshot(snap), snap["mail.password"] != "" && snap["mail.password"] != `""`, nil
}

func (h *handlers) renderMail(w http.ResponseWriter, r *http.Request, status int, form mailInput, fields map[string]string) {
	_, pwSet, err := h.currentMail(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	form.Password = ""
	form.ResendAPIKey = ""
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/mail.html", mailPage{Form: form, Fields: fields, TLSModes: tlsModes,
		PasswordSet: pwSet, APIKeySet: h.d.Settings.GetString(r.Context(), "mail.resend_api_key", "") != "", Configured: h.mail.Configured(r.Context())})
}

func (h *handlers) settingsMail(w http.ResponseWriter, r *http.Request) {
	snap, err := mailSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	form := mailFromSnapshot(snap)
	h.renderMail(w, r, http.StatusOK, form, nil)
}

func (h *handlers) settingsMailSave(w http.ResponseWriter, r *http.Request) {
	var in mailInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	vals, fields := h.validateMail(in)
	if len(fields) > 0 {
		h.renderMail(w, r, http.StatusUnprocessableEntity, in, fields)
		return
	}
	if err := h.saveMailSettings(r.Context(), vals, in.EditToken, nil); err != nil {
		if errors.Is(err, errMailConflict) {
			h.renderMail(w, r, http.StatusConflict, in, map[string]string{"conflict": "admin.mail.conflict"})
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/mail")
}

type mailTestResult struct {
	OK      bool
	Message string // i18n key
	To      string
}

// settingsMailTest sends the "test" template to the current user.
func (h *handlers) settingsMailTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	res := mailTestResult{OK: true, Message: "admin.mail.test_sent", To: u.Email}
	if err := h.sendTestMail(r, u); err != nil {
		h.d.Log.Error("admin: test mail", "err", err)
		res = mailTestResult{Message: "admin.mail.test_failed", To: u.Email}
	} else if !h.mail.Configured(ctx) {
		res.Message = "admin.mail.test_logged"
	}
	if r.Header.Get("HX-Request") == "true" {
		h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/system/fragments/mail_test_result.html", res)
		return
	}
	kind := "success"
	if !res.OK {
		kind = "error"
	}
	httpx.SetFlash(w, kind, res.Message)
	http.Redirect(w, r, "/admin/settings/mail", http.StatusSeeOther)
}

func (h *handlers) sendTestMail(r *http.Request, u *auth.User) error {
	locale := u.Locale
	if !validLocale(locale) {
		locale = i18n.Locale(r.Context())
	}
	name := u.Name
	if name == "" {
		name = u.Email
	}
	subject, html, text, err := h.mail.Render(locale, "test", map[string]any{"Name": name, "SiteTitle": h.siteTitle(r)})
	if err != nil {
		return err
	}
	if err := h.mail.Send(r.Context(), u.Email, subject, html, text); err != nil {
		return err
	}
	h.d.Audit.Record(r.Context(), "mail.test_sent", "user", u.ID, nil)
	return nil
}

// validateMail returns the settings to store and field errors.
func (h *handlers) validateMail(in mailInput) (map[string]any, map[string]string) {
	fields := map[string]string{}
	vals := map[string]any{}
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		provider = "smtp"
	}
	if provider != "smtp" && provider != "resend" {
		fields["provider"] = "admin.mail.error_provider"
	}
	vals["mail.provider"] = provider
	if len(in.ResendAPIKey) > 4096 || strings.ContainsAny(in.ResendAPIKey, " \t\r\n") {
		fields["resend_api_key"] = "admin.mail.error_key"
	} else if in.ClearResendAPIKey {
		vals["mail.resend_api_key"] = ""
	} else if in.ResendAPIKey != "" {
		enc, err := mailer.EncryptSecret(h.d.Config.SecretKey, in.ResendAPIKey)
		if err != nil {
			fields["resend_api_key"] = "admin.settings.error_encrypt"
		} else {
			vals["mail.resend_api_key"] = enc
		}
	}

	host := strings.TrimSpace(in.Host)
	if strings.ContainsAny(host, " /:@\t\r\n") || len(host) > 253 {
		fields["host"] = "admin.settings.error_host"
	}
	vals["mail.host"] = host
	port := 0
	if p := strings.TrimSpace(in.Port); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			fields["port"] = "admin.settings.error_port"
		}
		port = n
	}
	if port == 0 {
		port = 587
	}
	vals["mail.port"] = port
	vals["mail.username"] = strings.TrimSpace(in.Username)
	from := strings.TrimSpace(in.From)
	if from == "" && provider == "resend" {
		fields["from"] = "auth.error.email_invalid"
	}
	if from != "" {
		if a, err := mail.ParseAddress(from); err != nil || a.Address != from {
			fields["from"] = "auth.error.email_invalid"
		}
	}
	vals["mail.from"] = from
	vals["mail.from_name"] = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(in.FromName))
	if slices.Contains(tlsModes, in.TLS) {
		vals["mail.tls"] = in.TLS
	} else {
		fields["tls"] = "admin.settings.error_tls"
	}
	switch {
	case in.ClearPassword:
		vals["mail.password"] = ""
	case in.Password != "":
		enc, err := mailer.EncryptSecret(h.d.Config.SecretKey, in.Password)
		if err != nil {
			fields["password"] = "admin.settings.error_encrypt"
		} else {
			vals["mail.password"] = enc
		}
	}
	return vals, fields
}
