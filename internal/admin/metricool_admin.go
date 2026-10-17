package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

func (h *handlers) metricoolRoutes() {
	r := h.d.Router.Admin
	st := r.With(auth.RequirePerm(PermSettings))
	st.Get("/settings/metricool", h.metricoolSettings)
	st.Post("/settings/metricool", h.metricoolSave)
	st.Post("/settings/metricool/test", h.metricoolTest)
	composer := r.With(auth.RequirePerm("content.publish"))
	composer.Get("/metricool", h.metricoolCompose)
	composer.Post("/metricool/drafts", h.metricoolSubmit)
	h.d.Admin.AddNav(kernel.NavItem{ID: "settings.metricool", LabelKey: "admin.metricool.settings", Icon: "share-2", Href: "/admin/settings/metricool", Permission: PermSettings, Group: "integrations", Position: 810})
	h.d.Admin.AddNav(kernel.NavItem{ID: "content.metricool", LabelKey: "admin.metricool.title", Icon: "send", Href: "/admin/metricool", Permission: "content.publish", Group: "content", Position: 45})
}

type metricoolPage struct {
	Version, UserID, BlogID, TestStatus, TestedAt, Nonce, Message, ProviderID string
	SecretSet, Ready                                                          bool
	Input                                                                     metricoolInput
}

func (h *handlers) metricoolRender(w http.ResponseWriter, r *http.Request, status int, template string, data metricoolPage) {
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", template, data)
}
func metricoolView(c metricoolConfig) metricoolPage {
	return metricoolPage{Version: c.Version, UserID: c.UserID, BlogID: c.BlogID, SecretSet: c.Secret != "", TestStatus: c.TestStatus, TestedAt: c.TestedAt, Ready: c.Secret != "" && c.TestStatus == "pass"}
}
func (h *handlers) metricoolSettings(w http.ResponseWriter, r *http.Request) {
	c, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.metricoolRender(w, r, 200, "pages/settings/metricool.html", metricoolView(c))
}
func (h *handlers) metricoolError(w http.ResponseWriter, r *http.Request, err error) {
	status := 422
	if errors.Is(err, errMetricoolConflict) {
		status = 409
	}
	http.Error(w, "Metricool request rejected; refresh and review the settings or form.", status)
}
func (h *handlers) metricoolSave(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	c, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	expected := r.PostForm.Get("version")
	if expected != c.Version {
		h.metricoolError(w, r, errMetricoolConflict)
		return
	}
	c.UserID = strings.TrimSpace(r.PostForm.Get("user_id"))
	c.BlogID = strings.TrimSpace(r.PostForm.Get("blog_id"))
	token := r.PostForm.Get("token")
	if !metricoolID.MatchString(c.UserID) || !metricoolID.MatchString(c.BlogID) || !validConnectionSecret(token) {
		h.metricoolError(w, r, errMetricoolInvalid)
		return
	}
	if r.PostForm.Get("clear_secret") == "on" {
		c.Secret = ""
	}
	if token != "" {
		c.Secret, err = mailer.EncryptSecret(h.d.Config.SecretKey, token)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	c.TestStatus = ""
	c.TestedAt = ""
	if err = h.saveMetricool(r.Context(), c, expected); err != nil {
		h.metricoolError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings/metricool")
}
func (h *handlers) metricoolTest(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	c, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if c.Version != r.PostForm.Get("version") || c.Secret == "" {
		h.metricoolError(w, r, errMetricoolConflict)
		return
	}
	token, err := h.metricoolToken(c)
	if err != nil {
		http.Error(w, "Stored credential unavailable", 422)
		return
	}
	err = (metricoolClient{}).test(r.Context(), c, token)
	c.TestStatus = "pass"
	if err != nil {
		c.TestStatus = "fail"
	}
	c.TestedAt = time.Now().UTC().Format(time.RFC3339)
	if err = h.saveMetricool(r.Context(), c, c.Version); err != nil {
		h.metricoolError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings/metricool")
}
func (h *handlers) metricoolCompose(w http.ResponseWriter, r *http.Request) {
	c, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	data := metricoolView(c)
	data.Nonce = db.NewID()
	data.Input.Timezone = "UTC"
	h.metricoolRender(w, r, 200, "pages/metricool.html", data)
}
func (h *handlers) metricoolSubmit(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	c, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if c.Secret == "" || c.TestStatus != "pass" || r.PostForm.Get("version") != c.Version {
		h.metricoolError(w, r, errMetricoolConflict)
		return
	}
	in := metricoolInput{Text: strings.TrimSpace(r.PostForm.Get("text")), Network: r.PostForm.Get("network"), DateTime: r.PostForm.Get("date_time"), Timezone: r.PostForm.Get("timezone")}
	if len(in.DateTime) == 16 {
		in.DateTime += ":00"
	}
	if r.PostForm.Get("reviewed") != "on" || r.PostForm.Get("content_id") != "" || !validMetricoolInput(in, time.Now()) {
		h.metricoolError(w, r, errMetricoolInvalid)
		return
	}
	token, err := h.metricoolToken(c)
	if err != nil {
		http.Error(w, "Stored credential unavailable", 422)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Forbidden", 403)
		return
	}
	entry, fresh, err := h.claimMetricool(r.Context(), r.PostForm.Get("nonce"), user.ID, c, in)
	if err != nil {
		h.metricoolError(w, r, err)
		return
	}
	if fresh {
		providerID, sendErr := (metricoolClient{}).create(r.Context(), c, token, in)
		if sendErr == nil {
			if err = h.finishMetricool(r.Context(), entry, providerID); err == nil {
				entry.Status = "confirmed"
				entry.ProviderID = providerID
			}
		}
	}
	data := metricoolView(c)
	data.Nonce = entry.ID
	data.Input = in
	data.ProviderID = entry.ProviderID
	data.Message = "admin.metricool.uncertain"
	status := 502
	if entry.Status == "confirmed" {
		data.Message = "admin.metricool.confirmed"
		status = 200
	}
	h.metricoolRender(w, r, status, "pages/metricool.html", data)
}
