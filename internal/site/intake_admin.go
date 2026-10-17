package site

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const PermIntakeReview = "intake.review"

type intakeAdmin struct {
	h   *handlers
	tpl *httpx.Templates
}
type intakeAdminPage struct {
	Sources              []formSource
	Rows                 []intakeRecord
	Record               intakeRecord
	Error                string
	Page, Previous, Next int
	Enabled              bool
}

func registerIntakeAdmin(d *kernel.Deps, h *handlers) {
	d.Perms.Declare(PermIntakeReview, map[string]string{"en": "Review private reports", "ar": "مراجعة البلاغات الخاصة"})
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &intakeAdmin{h, tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "site.private_intake", LabelKey: "site.intake.admin_title", Href: "/admin/reports", Icon: "shield-check", Permission: PermIntakeReview, Group: "inbox", Position: 210})
	d.Admin.AddNav(kernel.NavItem{ID: "site.intake_settings", LabelKey: "site.intake.settings_title", Href: "/admin/settings/reports", Icon: "lock", Permission: "settings.manage", Group: "settings", Position: 740})
	r := d.Router.Admin.With(auth.RequirePerm(PermIntakeReview))
	r.Get("/reports", a.list)
	r.Get("/reports/{id}", a.show)
	r.Post("/reports/{id}", a.update)
	st := d.Router.Admin.With(auth.RequirePerm("settings.manage"))
	st.Get("/settings/reports", a.settings)
	st.Post("/settings/reports", a.saveSettings)
}
func (a *intakeAdmin) render(w http.ResponseWriter, r *http.Request, status int, name string, data intakeAdminPage) {
	intakeHeaders(w)
	a.tpl.RenderWith(w, r, status, "admin", name, data)
}
func (a *intakeAdmin) fail(w http.ResponseWriter, r *http.Request, err error) {
	intakeHeaders(w)
	if errors.Is(err, sql.ErrNoRows) {
		a.tpl.RenderError(w, r, 404)
		return
	}
	a.h.d.Log.Error("intake: private review operation failed")
	a.tpl.RenderError(w, r, 500)
}
func (a *intakeAdmin) list(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 || page > 100000 {
		page = 1
	}
	data := intakeAdminPage{Page: page, Rows: []intakeRecord{}, Enabled: a.h.intakeEnabled(r)}
	data.Sources = a.h.intakeSources(r.Context(), data.Enabled)
	rows, err := a.h.d.DB.QueryContext(r.Context(), `SELECT id,kind,status,created_at,updated_at,source_path FROM private_intake ORDER BY created_at DESC,id DESC LIMIT 26 OFFSET ?`, (page-1)*25)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var row intakeRecord
		if err = rows.Scan(&row.ID, &row.Kind, &row.Status, &row.CreatedAt, &row.UpdatedAt, &row.SourcePath); err != nil {
			a.fail(w, r, err)
			return
		}
		row.Source = a.h.intakeSource(r.Context(), row.SourcePath, row.Kind)
		data.Rows = append(data.Rows, row)
	}
	if err = rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	if len(data.Rows) > 25 {
		data.Next = page + 1
		data.Rows = data.Rows[:25]
	}
	if page > 1 {
		data.Previous = page - 1
	}
	a.render(w, r, 200, "pages/site/private_reports.html", data)
}
func (a *intakeAdmin) show(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !intakeID.MatchString(id) {
		a.fail(w, r, sql.ErrNoRows)
		return
	}
	row, err := a.h.loadIntake(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.h.d.Audit.Record(r.Context(), "intake.viewed", "private_intake", id, nil)
	a.render(w, r, 200, "pages/site/private_report.html", intakeAdminPage{Record: row})
}
func (a *intakeAdmin) update(w http.ResponseWriter, r *http.Request) {
	intakeHeaders(w)
	if r.ParseForm() != nil {
		a.tpl.RenderError(w, r, 400)
		return
	}
	id := chi.URLParam(r, "id")
	if !intakeID.MatchString(id) {
		a.fail(w, r, sql.ErrNoRows)
		return
	}
	row, err := a.h.loadIntake(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	expected := r.PostForm.Get("version")
	if expected != row.Version {
		a.render(w, r, 409, "pages/site/private_report.html", intakeAdminPage{Record: row, Error: "site.intake.conflict"})
		return
	}
	status := r.PostForm.Get("status")
	note := strings.TrimSpace(r.PostForm.Get("note"))
	if !validIntakeStatus(status) || !utf8.ValidString(note) || utf8.RuneCountInString(note) > 3000 || strings.ContainsRune(note, 0) || len(row.Data.Notes) >= 100 {
		a.render(w, r, 422, "pages/site/private_report.html", intakeAdminPage{Record: row, Error: "site.intake.review_invalid"})
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		a.tpl.RenderError(w, r, 403)
		return
	}
	row.Status = status
	row.Data.Notes = append(row.Data.Notes, intakeNote{By: user.ID, At: db.Now(), Status: status, Text: note})
	if err = a.h.updateIntake(r.Context(), row, expected); err != nil {
		if errors.Is(err, errIntakeConflict) {
			a.render(w, r, 409, "pages/site/private_report.html", intakeAdminPage{Record: row, Error: "site.intake.conflict"})
			return
		}
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/reports/"+id, 303)
}
func validIntakeStatus(status string) bool {
	switch status {
	case "new", "reviewing", "needs_information", "closed":
		return true
	}
	return false
}
func (a *intakeAdmin) settings(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, 200, "pages/site/private_report_settings.html", intakeAdminPage{Enabled: a.h.intakeEnabled(r)})
}
func (a *intakeAdmin) saveSettings(w http.ResponseWriter, r *http.Request) {
	intakeHeaders(w)
	if r.ParseForm() != nil {
		a.tpl.RenderError(w, r, 400)
		return
	}
	enabled := r.PostForm.Get("enabled") == "yes"
	if r.PostForm.Get("previous") != strconv.FormatBool(a.h.intakeEnabled(r)) {
		a.render(w, r, 409, "pages/site/private_report_settings.html", intakeAdminPage{Enabled: a.h.intakeEnabled(r), Error: "site.intake.conflict"})
		return
	}
	err := db.InTx(r.Context(), a.h.d.DB, func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRowContext(r.Context(), `SELECT value_json FROM settings WHERE key='intake.enabled'`).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			current = "false"
		} else if err != nil {
			return err
		}
		if current != r.PostForm.Get("previous") {
			return errIntakeConflict
		}
		if err = a.h.d.Settings.SetTx(r.Context(), tx, "intake.enabled", enabled); err != nil {
			return err
		}
		a.h.d.Audit.RecordTx(r.Context(), tx, "intake.settings", "settings", "intake.enabled", map[string]any{"enabled": enabled})
		return nil
	})
	if errors.Is(err, errIntakeConflict) {
		a.render(w, r, 409, "pages/site/private_report_settings.html", intakeAdminPage{Enabled: a.h.intakeEnabled(r), Error: "site.intake.conflict"})
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/settings/reports", 303)
}
