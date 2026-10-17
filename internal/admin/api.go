package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

const maxAPIBody = 1 << 20

// userDTO is the API representation of a user.
type userDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Locale      string `json:"locale"`
	Status      string `json:"status"`
	LastLoginAt string `json:"last_login_at"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toDTO(u *auth.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Locale: u.Locale, Status: u.Status,
		LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
}

// userPatchJSON is the PATCH /users/{id} body; absent fields are unchanged.
type userPatchJSON struct {
	Name     *string `json:"name"`
	Email    *string `json:"email"`
	Role     *string `json:"role"`
	Locale   *string `json:"locale"`
	Status   *string `json:"status"`
	Password *string `json:"password"`
}

// SettingsDTO is the GET /v1/settings response (and the PATCH response).
// mail.password is never returned; mail.password_set reports whether one
// is stored.
type SettingsDTO struct {
	SiteTitle           string `json:"site.title"`
	SiteTagline         string `json:"site.tagline"`
	SiteLocale          string `json:"site.locale"`
	SiteTimezone        string `json:"site.timezone"`
	SiteBaseURL         string `json:"site.base_url"`
	SiteTheme           string `json:"site.theme"`
	SiteFrontPage       string `json:"site.front_page"`
	SiteLogo            string `json:"site.logo"`
	ContactEmail        string `json:"contact.email"`
	PostsPerPage        int    `json:"site.posts_per_page"`
	MailProvider        string `json:"mail.provider"`
	MailResendAPIKeySet bool   `json:"mail.resend_api_key_set"`
	MailHost            string `json:"mail.host"`
	MailPort            int    `json:"mail.port"`
	MailUsername        string `json:"mail.username"`
	MailFrom            string `json:"mail.from"`
	MailFromName        string `json:"mail.from_name"`
	MailTLS             string `json:"mail.tls"`
	MailPasswordSet     bool   `json:"mail.password_set"`
}

// SettingsPatchDTO documents the PATCH /v1/settings body: send only the
// keys to change. "mail.password": null clears the stored password, ""
// keeps it, any other string replaces it (stored encrypted). Unknown keys
// are rejected.
type SettingsPatchDTO struct {
	SiteTitle        *string `json:"site.title,omitempty"`
	SiteTagline      *string `json:"site.tagline,omitempty"`
	SiteLocale       *string `json:"site.locale,omitempty"`
	SiteTimezone     *string `json:"site.timezone,omitempty"`
	SiteBaseURL      *string `json:"site.base_url,omitempty"`
	SiteTheme        *string `json:"site.theme,omitempty"`
	SiteFrontPage    *string `json:"site.front_page,omitempty"`
	SiteLogo         *string `json:"site.logo,omitempty"`
	ContactEmail     *string `json:"contact.email,omitempty"`
	PostsPerPage     *int    `json:"site.posts_per_page,omitempty"`
	MailProvider     *string `json:"mail.provider,omitempty"`
	MailResendAPIKey *string `json:"mail.resend_api_key,omitempty"`
	MailHost         *string `json:"mail.host,omitempty"`
	MailPort         *int    `json:"mail.port,omitempty"`
	MailUsername     *string `json:"mail.username,omitempty"`
	MailPassword     *string `json:"mail.password,omitempty"`
	MailFrom         *string `json:"mail.from,omitempty"`
	MailFromName     *string `json:"mail.from_name,omitempty"`
	MailTLS          *string `json:"mail.tls,omitempty"`
}

// opResult is the response of action endpoints (job retry, user delete).
type opResult struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "queued" (retry) | "deleted" (delete)
}

func (h *handlers) apiOperations() {
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	pageParams := []kernel.APIParam{{Name: "page", In: "query", Type: "integer"}, {Name: "per_page", In: "query", Type: "integer"}}
	ops := []kernel.APIOperation{
		{Method: "GET", Path: "/v1/settings", Summary: "Read site and mail settings (secrets are never returned)", Tags: []string{"settings"},
			Scopes: []string{PermSettings}, Response: SettingsDTO{}, Handler: h.apiSettingsGet},
		{Method: "PATCH", Path: "/v1/settings", Summary: "Update site and mail settings", Tags: []string{"settings"},
			Scopes: []string{PermSettings}, Request: SettingsPatchDTO{}, Response: SettingsDTO{}, Handler: h.apiSettingsPatch},
		{Method: "GET", Path: "/v1/users", Summary: "List users", Tags: []string{"users"}, Scopes: []string{PermUsers},
			Params:   append([]kernel.APIParam{{Name: "q", In: "query", Type: "string"}, {Name: "role", In: "query", Type: "string"}}, pageParams...),
			Response: []userDTO{}, Handler: h.apiUsersList},
		{Method: "POST", Path: "/v1/users", Summary: "Create a user", Tags: []string{"users"}, Scopes: []string{PermUsers},
			Request: userInput{}, Response: userDTO{}, Handler: h.apiUserCreate},
		{Method: "GET", Path: "/v1/users/{id}", Summary: "Get a user", Tags: []string{"users"}, Scopes: []string{PermUsers},
			Params: idParam, Response: userDTO{}, Handler: h.apiUserGet},
		{Method: "PATCH", Path: "/v1/users/{id}", Summary: "Update a user", Tags: []string{"users"}, Scopes: []string{PermUsers},
			Params: idParam, Request: userPatchJSON{}, Response: userDTO{}, Handler: h.apiUserPatch},
		{Method: "DELETE", Path: "/v1/users/{id}", Summary: "Delete a user", Tags: []string{"users"}, Scopes: []string{PermUsers},
			Params: idParam, Response: opResult{}, Handler: h.apiUserDelete},
		{Method: "GET", Path: "/v1/jobs", Summary: "List jobs", Tags: []string{"system"}, Scopes: []string{PermHealth},
			Params: append([]kernel.APIParam{{Name: "status", In: "query", Type: "string"}}, pageParams...), Response: []jobs.Job{}, Handler: h.apiJobsList},
		{Method: "POST", Path: "/v1/jobs/{id}/retry", Summary: "Retry a failed or dead job", Tags: []string{"system"}, Scopes: []string{PermHealth},
			Params: idParam, Response: opResult{}, Handler: h.apiJobRetry},
		{Method: "GET", Path: "/v1/audit", Summary: "List audit log entries", Tags: []string{"system"}, Scopes: []string{PermHealth},
			Params: append([]kernel.APIParam{{Name: "actor_kind", In: "query", Type: "string"}, {Name: "actor_id", In: "query", Type: "string"},
				{Name: "action", In: "query", Type: "string", Description: "action prefix"}, {Name: "target_kind", In: "query", Type: "string"},
				{Name: "target_id", In: "query", Type: "string"}, {Name: "since", In: "query", Type: "string"}, {Name: "until", In: "query", Type: "string"}}, pageParams...),
			Response: []audit.Entry{}, Handler: h.apiAuditList},
		{Method: "GET", Path: "/v1/system/health", Summary: "System health report", Tags: []string{"system"}, Scopes: []string{PermHealth},
			Response: healthReport{}, Handler: h.apiHealth},
	}
	for _, op := range ops {
		h.d.API.Add(op)
	}
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAPIBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// badRequest answers an undecodable body with a translated generic
// message; the decoder error is only logged.
func (h *handlers) badRequest(w http.ResponseWriter, r *http.Request, err error) {
	h.d.Log.Info("admin: api bad request", "err", err, "path", r.URL.Path, "request_id", httpx.RequestIDFromContext(r.Context()))
	httpx.Error(w, http.StatusBadRequest, "bad_request", h.tr(r, "admin.api.bad_request"), nil)
}

func (h *handlers) validationFailed(w http.ResponseWriter, r *http.Request, fields map[string]string) {
	httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", h.tr(r, "admin.api.validation_failed"), h.translateFields(r, fields))
}

// --- settings ---

// apiSettingsGet returns the general and mail settings; mail.password is
// replaced by mail.password_set.
func (h *handlers) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermSettings) {
		return
	}
	g := h.currentGeneral(r)
	m, pwSet, err := h.currentMail(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	port, _ := strconv.Atoi(m.Port)
	httpx.JSON(w, http.StatusOK, SettingsDTO{
		SiteTitle: g.Title, SiteTagline: g.Tagline, SiteLocale: g.Locale, SiteTimezone: g.Timezone,
		SiteBaseURL: g.BaseURL, SiteTheme: g.Theme, SiteFrontPage: g.FrontPage,
		SiteLogo: g.Logo, ContactEmail: g.ContactEmail, PostsPerPage: h.postsPerPage(r),
		MailProvider: m.Provider, MailResendAPIKeySet: h.d.Settings.GetString(r.Context(), "mail.resend_api_key", "") != "",
		MailHost: m.Host, MailPort: port, MailUsername: m.Username, MailFrom: m.From,
		MailFromName: m.FromName, MailTLS: m.TLS, MailPasswordSet: pwSet,
	}, nil)
}

// apiSettingsPatch merges the given keys into the current general / mail
// settings and validates the result like the admin forms.
func (h *handlers) apiSettingsPatch(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermSettings) {
		return
	}
	var body map[string]json.RawMessage
	if err := decodeJSON(r, &body); err != nil {
		h.badRequest(w, r, err)
		return
	}
	generalState, err := generalSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mailState, err := mailSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	g := h.generalFromSnapshot(generalState)
	m := mailFromSnapshot(mailState)
	general := map[string]*string{"site.title": &g.Title, "site.tagline": &g.Tagline, "site.locale": &g.Locale, "site.timezone": &g.Timezone,
		"site.base_url": &g.BaseURL, "site.theme": &g.Theme, "site.front_page": &g.FrontPage,
		"site.logo": &g.Logo, "contact.email": &g.ContactEmail, "site.posts_per_page": &g.PostsPerPage}
	mailF := map[string]*string{"mail.provider": &m.Provider, "mail.resend_api_key": &m.ResendAPIKey, "mail.host": &m.Host, "mail.port": &m.Port, "mail.username": &m.Username, "mail.password": &m.Password,
		"mail.from": &m.From, "mail.from_name": &m.FromName, "mail.tls": &m.TLS}
	touchedGeneral, touchedMail := false, false
	fields := map[string]string{}
	for k, raw := range body {
		dst, isGeneral := general[k]
		if !isGeneral {
			dst = mailF[k]
		}
		if dst == nil {
			fields[k] = "admin.api.unknown_setting"
			continue
		}
		if (k == "mail.password" || k == "mail.resend_api_key") && string(raw) == "null" {
			if k == "mail.password" {
				m.ClearPassword = true
			} else {
				m.ClearResendAPIKey = true
			}
			touchedMail = true
			continue
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			var n json.Number
			if (k != "mail.port" && k != "site.posts_per_page") || json.Unmarshal(raw, &n) != nil {
				fields[k] = "admin.api.invalid_value"
				continue
			}
			*dst = n.String()
		}
		touchedGeneral = touchedGeneral || isGeneral
		touchedMail = touchedMail || !isGeneral
	}
	vals := map[string]any{}
	if touchedGeneral {
		v, f := h.validateGeneral(r.Context(), g)
		if e, ok := f["contact_email"]; ok {
			delete(f, "contact_email")
			fields["contact.email"] = e
		}
		mergeFields(fields, f, "site.")
		for k, x := range v {
			vals[k] = x
		}
	}
	if touchedMail {
		v, f := h.validateMail(m)
		mergeFields(fields, f, "mail.")
		for k, x := range v {
			vals[k] = x
		}
	}
	if len(fields) > 0 {
		h.validationFailed(w, r, fields)
		return
	}
	if len(vals) > 0 {
		var generalExpected *string
		if touchedGeneral {
			token := generalEditToken(generalState)
			generalExpected = &token
		}
		var saveErr error
		if touchedMail {
			saveErr = h.saveMailSettings(r.Context(), vals, m.EditToken, generalExpected)
		} else {
			saveErr = h.saveSettingsGuarded(r.Context(), vals, generalExpected)
		}
		if errors.Is(saveErr, errMailConflict) || errors.Is(saveErr, errGeneralConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", h.tr(r, "admin.mail.conflict"), nil)
			return
		}
		if saveErr != nil {
			h.serverError(w, r, saveErr)
			return
		}
	}
	h.apiSettingsGet(w, r)
}

// mergeFields copies form field errors into dst keyed by setting name.
func mergeFields(dst, src map[string]string, prefix string) {
	for k, v := range src {
		dst[prefix+k] = v
	}
}

// --- users ---

func (h *handlers) apiUsersList(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	q := r.URL.Query()
	page, perPage := pageParams(q)
	list, total, err := listUsers(r.Context(), h.d.DB, userQuery{Search: strings.TrimSpace(q.Get("q")), Role: q.Get("role"), Page: page, PerPage: perPage})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	out := make([]userDTO, len(list))
	for i := range list {
		out[i] = toDTO(&list[i])
	}
	httpx.JSON(w, http.StatusOK, out, httpx.Meta{Total: total, Page: page, PerPage: perPage})
}

func (h *handlers) apiUserCreate(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	var in userInput
	if err := decodeJSON(r, &in); err != nil {
		h.badRequest(w, r, err)
		return
	}
	u, fields, err := h.createUser(r.Context(), in)
	switch {
	case err != nil:
		h.serverError(w, r, err)
	case len(fields) > 0:
		h.validationFailed(w, r, fields)
	default:
		httpx.JSON(w, http.StatusCreated, toDTO(u), nil)
	}
}

// apiUser loads the {id} user or writes 404.
func (h *handlers) apiUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u, err := h.users.Get(r.Context(), urlID(r))
	if errors.Is(err, auth.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
		return nil, false
	}
	if err != nil {
		h.serverError(w, r, err)
		return nil, false
	}
	return u, true
}

func (h *handlers) apiUserGet(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	if u, ok := h.apiUser(w, r); ok {
		httpx.JSON(w, http.StatusOK, toDTO(u), nil)
	}
}

func (h *handlers) apiUserPatch(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	cur, ok := h.apiUser(w, r)
	if !ok {
		return
	}
	var in userPatchJSON
	if err := decodeJSON(r, &in); err != nil {
		h.badRequest(w, r, err)
		return
	}
	u, fields, err := h.updateUser(r.Context(), cur, auth.UserPatch{Name: in.Name, Email: in.Email, Role: in.Role, Locale: in.Locale, Status: in.Status, Password: in.Password})
	switch {
	case err != nil:
		h.serverError(w, r, err)
	case len(fields) > 0:
		h.validationFailed(w, r, fields)
	default:
		httpx.JSON(w, http.StatusOK, toDTO(u), nil)
	}
}

func (h *handlers) apiUserDelete(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermUsers) {
		return
	}
	err := h.deleteUser(r.Context(), urlID(r))
	switch {
	case errors.Is(err, errSelf), errors.Is(err, errLastAdmin):
		httpx.Error(w, http.StatusConflict, "conflict", h.tr(r, guardKey(err)), nil)
	case errors.Is(err, auth.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
	case err != nil:
		h.serverError(w, r, err)
	default:
		httpx.JSON(w, http.StatusOK, opResult{ID: urlID(r), Status: "deleted"}, nil)
	}
}

// --- jobs, audit, health ---

func (h *handlers) apiJobsList(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermHealth) {
		return
	}
	q := r.URL.Query()
	status, ok := normalizeJobStatus(q.Get("status"))
	if !ok {
		h.validationFailed(w, r, map[string]string{"status": "core.validation.oneof"})
		return
	}
	page, perPage := pageParams(q)
	list, total, err := listJobs(r.Context(), h.d.DB, status, page, perPage)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list, httpx.Meta{Total: total, Page: page, PerPage: perPage})
}

func (h *handlers) apiJobRetry(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermHealth) {
		return
	}
	err := h.retryJob(r.Context(), urlID(r))
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "admin.jobs.retry_missing"), nil)
	case err != nil:
		h.serverError(w, r, err)
	default:
		httpx.JSON(w, http.StatusOK, opResult{ID: urlID(r), Status: "queued"}, nil)
	}
}

func (h *handlers) apiAuditList(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermHealth) {
		return
	}
	f := auditFilterFrom(r.URL.Query())
	fields := map[string]string{}
	if _, ok := bound(f.Since, false); !ok {
		fields["since"] = "admin.audit.error_date"
	}
	if _, ok := bound(f.Until, true); !ok {
		fields["until"] = "admin.audit.error_date"
	}
	if len(fields) > 0 {
		h.validationFailed(w, r, fields)
		return
	}
	list, total, err := listAudit(r.Context(), h.d.DB, f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list, httpx.Meta{Total: total, Page: f.Page, PerPage: f.PerPage})
}

func (h *handlers) apiHealth(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermHealth) {
		return
	}
	rep, err := h.healthReport(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rep, nil)
}
