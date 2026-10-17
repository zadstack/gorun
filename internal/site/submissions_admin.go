package site

import (
	"encoding/csv"
	"errors"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	// PermSubmissions guards System → Form submissions (an existing
	// permission: no new grant needed).
	PermSubmissions = "settings.manage"
	submissionsPath = "/system/form-submissions"
)

type submissionsPage struct {
	Sources       []formSource
	Items         []Submission
	View          string
	Counts        map[string]int // per view; the current view's is the pager total
	Page, PerPage int
	Settings      formConfig
}

type submissionAdmin struct {
	h   *handlers
	tpl *httpx.Templates
}

// registerSubmissionsAdmin adds System → Form submissions: an inbox (all /
// new / spam), a detail page that marks the message read, the private
// attachment download, status, resend, delete, CSV export and the form
// settings. Without the admin renderer it does nothing.
func registerSubmissionsAdmin(d *kernel.Deps, h *handlers) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &submissionAdmin{h: h, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "system.form_submissions", LabelKey: "site.submissions.nav", Icon: "inbox",
		Href: "/admin" + submissionsPath, Permission: PermSubmissions, Group: "inbox", Position: 200})
	r := d.Router.Admin.With(auth.RequirePerm(PermSubmissions))
	r.Get(submissionsPath, a.list)
	r.Get(submissionsPath+".csv", a.export)
	r.Post(submissionsPath+"/settings", a.saveSettings)
	r.Get(submissionsPath+"/{id}", a.show)
	r.Get(submissionsPath+"/{id}/attachment", a.attachment)
	r.Post(submissionsPath+"/{id}/status", a.status)
	r.Post(submissionsPath+"/{id}/resend", a.resend)
	r.Post(submissionsPath+"/{id}/delete", a.remove)
}

func (a *submissionAdmin) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNoSubmission) {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	a.h.d.Log.Error("site: form submissions admin", "err", err)
	a.tpl.RenderError(w, r, http.StatusInternalServerError)
}

func (a *submissionAdmin) back(w http.ResponseWriter, r *http.Request, to, kind, msg string) {
	httpx.SetFlash(w, kind, msg)
	http.Redirect(w, r, "/admin"+submissionsPath+to, http.StatusSeeOther)
}

func (a *submissionAdmin) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	view := normalizeView(r.URL.Query().Get("view"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	counts, err := a.h.countSubmissions(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data := submissionsPage{View: view, Page: max(page, 1), PerPage: submissionsPerPage, Counts: counts,
		Items: []Submission{}, Settings: a.h.formConfig(ctx)}
	sources, err := a.h.contactSources(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data.Sources = sources
	if err := a.h.eachSubmission(ctx, view, data.Page, submissionsPerPage, func(s Submission) error {
		s.Source = resolveFormSource(formSourcePath(s.Page), sources)
		data.Items = append(data.Items, s)
		return nil
	}); err != nil {
		a.fail(w, r, err)
		return
	}
	a.tpl.Render(w, r, "pages/site/form_submissions.html", data)
}

// show renders one submission and marks it read when it was new.
func (a *submissionAdmin) show(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := a.h.getSubmission(ctx, chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if s.Status == statusNew {
		if err := a.h.setStatus(ctx, s.ID, statusRead); err != nil {
			a.fail(w, r, err)
			return
		}
		s.Status = statusRead
	}
	sources, err := a.h.contactSources(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	s.Source = resolveFormSource(formSourcePath(s.Page), sources)
	a.tpl.Render(w, r, "pages/site/form_submission.html", s)
}

// attachment sends the stored file as a download (never inline).
func (a *submissionAdmin) attachment(w http.ResponseWriter, r *http.Request) {
	s, err := a.h.getSubmission(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := os.Open(a.h.uploadPath(s.ID))
	if s.AttachmentSize == 0 || errors.Is(err, os.ErrNotExist) {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", s.AttachmentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": s.AttachmentName}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (a *submissionAdmin) status(w http.ResponseWriter, r *http.Request) {
	id, status := chi.URLParam(r, "id"), r.PostFormValue("status")
	if err := a.h.setStatus(r.Context(), id, status); err != nil {
		if errors.Is(err, errNoSubmission) {
			a.fail(w, r, err)
			return
		}
		a.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	a.h.d.Audit.Record(r.Context(), "site.form.status", "form_submission", id, map[string]any{"status": status})
	to := "/" + id
	if status == statusSpam {
		to = ""
	}
	a.back(w, r, to, "success", "site.submissions.flash.status")
}

// resend mails the submission again now (not through the job queue).
func (a *submissionAdmin) resend(w http.ResponseWriter, r *http.Request) {
	s, err := a.h.getSubmission(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	switch a.h.deliver(r.Context(), s.FormSubmission) {
	case deliverySent:
		a.back(w, r, "/"+s.ID, "success", "site.submissions.flash.sent")
	case deliveryOff:
		a.back(w, r, "/"+s.ID, "error", "site.submissions.flash.notify_off")
	default:
		a.back(w, r, "/"+s.ID, "error", "site.submissions.flash.send_failed")
	}
}

func (a *submissionAdmin) remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.h.deleteSubmission(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.h.d.Audit.Record(r.Context(), "site.form.delete", "form_submission", id, nil)
	a.back(w, r, "", "success", "site.submissions.flash.deleted")
}

// saveSettings stores the four form settings from the inbox's settings panel.
func (a *submissionAdmin) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in := map[string]string{
		SettingFormsNotify:     strconv.FormatBool(r.PostFormValue("notify") == "1"),
		SettingFormsRecipients: r.PostFormValue("recipients"),
		SettingAttachments:     strconv.FormatBool(r.PostFormValue("attachments") == "1"),
		SettingAttachmentMaxMB: r.PostFormValue("max_mb"),
	}
	values := map[string]any{}
	for _, k := range FormSettingKeys {
		v, err := ValidateFormSetting(k, in[k])
		if err != nil {
			code := "site.submissions.settings.error_max_mb"
			if k == SettingFormsRecipients {
				code = "site.submissions.settings.error_recipients"
			}
			a.back(w, r, "", "error", code)
			return
		}
		values[k] = v
	}
	for _, k := range FormSettingKeys {
		if err := a.h.d.Settings.Set(ctx, k, values[k]); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	a.h.d.Audit.Record(ctx, "site.form.settings", "settings", "forms", map[string]any{"keys": FormSettingKeys})
	a.back(w, r, "", "success", "site.submissions.flash.settings")
}

// export writes the view as CSV (UTF-8 with BOM, so spreadsheet apps read
// Arabic). Cells starting with = + - @ are prefixed with ' so they are
// never evaluated as formulas.
func (a *submissionAdmin) export(w http.ResponseWriter, r *http.Request) {
	view := normalizeView(r.URL.Query().Get("view"))
	name := "form-submissions-" + view + "-" + time.Now().UTC().Format("20060102") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "created_at", "form", "status", "name", "email", "message", "page", "attachment", "delivery", "ip"})
	n := 0
	err := a.h.eachSubmission(r.Context(), view, 1, 0, func(s Submission) error {
		row := []string{s.ID, s.SubmittedAt, s.Form, s.Status, s.Name, s.Email, s.Message, formSourcePath(s.Page), s.AttachmentName, s.Delivery, s.IP}
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		n++
		return cw.Write(row)
	})
	cw.Flush()
	if err = errors.Join(err, cw.Error()); err != nil {
		a.h.d.Log.Error("site: export submissions", "err", err) // headers are sent: the file ends early
	}
	a.h.d.Audit.Record(r.Context(), "site.form.export", "form_submission", view, map[string]any{"rows": n})
}

func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
